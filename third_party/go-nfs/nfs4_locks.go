package nfs

import (
	"encoding/binary"
	"math"
	"sync"
	"time"
)

// NFSv4.0 advisory byte-range locking (RFC 7530 sections 9 and 16.10-16.12).
//
// Locks are advisory only: they are never enforced against READ or WRITE,
// matching the common cloud file-gateway model. All state is in-memory and
// owned by this single server instance, which is sufficient because one
// server owns one export.
//
// Lock state uses real bookkeeping (owners, ranges, stateids with
// incrementing seqids) even though the rest of this v4.0 implementation is
// intentionally stateless: the Linux client round-trips lock stateids and
// expects POSIX range semantics (same-owner overlap replaces, different-owner
// conflicts are denied with the conflicting lock described).

const (
	readLT  uint32 = 1
	writeLT uint32 = 2
	// Blocking variants are treated as their non-blocking forms: the server
	// answers DENIED and the client polls, which RFC 7530 permits.
	readWLT  uint32 = 3
	writeWLT uint32 = 4

	// nfs4LengthEOF as a lock length means "to end of file".
	nfs4LengthEOF = math.MaxUint64

	// Caps mirror the managed-service model (Amazon S3 Files quotas):
	// bounded state per file and per client keeps a misbehaving or leaky
	// client from growing server memory without bound.
	maxLocksPerFile   = 512
	maxLocksPerClient = 8192

	// Lock state whose client has not renewed within this many lease
	// periods is expired lazily. RFC allows reclaiming after one lease
	// period; being generous costs little and forgives slow clients.
	lockLeaseGracePeriods = 3
)

// lockOwnerID identifies a lock owner: the client's short-form id plus the
// client-provided opaque owner bytes (RFC 7530 lock_owner4).
type lockOwnerID struct {
	clientID uint64
	owner    string
}

// lockRange is a held byte range: [start, end) with end == nfs4LengthEOF
// meaning to end of file. Advisory READ/WRITE type per POSIX.
type lockRange struct {
	start, end uint64
	lockType   uint32
}

func (r lockRange) overlaps(o lockRange) bool {
	return r.start < o.end && o.start < r.end
}

// lockState is the per-(owner, file) locking state behind one lock stateid.
type lockState struct {
	owner  lockOwnerID
	path   string
	other  [nfs4OtherSize]byte
	seqid  uint32
	ranges []lockRange
}

type nfs4LockManager struct {
	mu sync.Mutex
	// states indexes every lock stateid by its "other" field.
	states map[[nfs4OtherSize]byte]*lockState
	// byFile indexes lock states holding ranges on a path.
	byFile map[string]map[*lockState]struct{}
	// byOwner finds the existing state for (owner, path) on repeat LOCKs
	// that present the open stateid again.
	byOwner map[lockOwnerID]map[string]*lockState
	// clientLocks counts held ranges per client for the client cap.
	clientLocks map[uint64]int
	// clientSeen tracks lease renewal for lazy expiry.
	clientSeen map[uint64]time.Time
	// counter feeds unique stateid "other" values.
	counter uint64

	now func() time.Time
}

func newNFS4LockManager() *nfs4LockManager {
	return &nfs4LockManager{
		states:      make(map[[nfs4OtherSize]byte]*lockState),
		byFile:      make(map[string]map[*lockState]struct{}),
		byOwner:     make(map[lockOwnerID]map[string]*lockState),
		clientLocks: make(map[uint64]int),
		clientSeen:  make(map[uint64]time.Time),
		now:         time.Now,
	}
}

// lockDenied describes the conflicting lock for a DENIED response.
type lockDenied struct {
	offset   uint64
	length   uint64
	lockType uint32
	owner    lockOwnerID
}

func rangeToDenied(r lockRange, owner lockOwnerID) *lockDenied {
	length := uint64(nfs4LengthEOF)
	if r.end != nfs4LengthEOF {
		length = r.end - r.start
	}
	return &lockDenied{offset: r.start, length: length, lockType: r.lockType, owner: owner}
}

// makeRange validates RFC 7530 offset/length rules.
func makeRange(offset, length uint64, lockType uint32) (lockRange, nfs4Status) {
	if length == 0 {
		return lockRange{}, nfs4ErrInval
	}
	end := uint64(nfs4LengthEOF)
	if length != nfs4LengthEOF {
		if offset > math.MaxUint64-length {
			return lockRange{}, nfs4ErrInval
		}
		end = offset + length
	}
	normalized := lockType
	if normalized == readWLT {
		normalized = readLT
	}
	if normalized == writeWLT {
		normalized = writeLT
	}
	if normalized != readLT && normalized != writeLT {
		return lockRange{}, nfs4ErrInval
	}
	return lockRange{start: offset, end: end, lockType: normalized}, nfs4OK
}

func rangesConflict(a, b lockRange) bool {
	return a.overlaps(b) && (a.lockType == writeLT || b.lockType == writeLT)
}

// touchClient records lease activity and lazily expires state from clients
// that stopped renewing.
func (lm *nfs4LockManager) touchClient(clientID uint64) {
	now := lm.now()
	lm.clientSeen[clientID] = now

	deadline := now.Add(-lockLeaseGracePeriods * nfs4LeaseTimeSecs * time.Second)
	for client, seen := range lm.clientSeen {
		if client == clientID || !seen.Before(deadline) {
			continue
		}
		lm.expireClientLocked(client)
	}
}

// renewClient marks lease renewal without creating state for unknown clients.
func (lm *nfs4LockManager) renewClient(clientID uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if _, known := lm.clientSeen[clientID]; known {
		lm.touchClient(clientID)
	}
}

func (lm *nfs4LockManager) expireClientLocked(clientID uint64) {
	for other, st := range lm.states {
		if st.owner.clientID != clientID {
			continue
		}
		lm.dropStateLocked(other, st)
	}
	delete(lm.clientSeen, clientID)
	delete(lm.clientLocks, clientID)
}

func (lm *nfs4LockManager) dropStateLocked(other [nfs4OtherSize]byte, st *lockState) {
	lm.clientLocks[st.owner.clientID] -= len(st.ranges)
	if lm.clientLocks[st.owner.clientID] <= 0 {
		delete(lm.clientLocks, st.owner.clientID)
	}
	delete(lm.states, other)
	if files, ok := lm.byOwner[st.owner]; ok {
		delete(files, st.path)
		if len(files) == 0 {
			delete(lm.byOwner, st.owner)
		}
	}
	if states, ok := lm.byFile[st.path]; ok {
		delete(states, st)
		if len(states) == 0 {
			delete(lm.byFile, st.path)
		}
	}
}

func (lm *nfs4LockManager) newStateLocked(owner lockOwnerID, path string) *lockState {
	st := &lockState{owner: owner, path: path}
	lm.counter++
	binary.BigEndian.PutUint32(st.other[0:4], 0x4C4F434B) // "LOCK"
	binary.BigEndian.PutUint64(st.other[4:12], lm.counter)
	lm.states[st.other] = st
	if lm.byOwner[owner] == nil {
		lm.byOwner[owner] = make(map[string]*lockState)
	}
	lm.byOwner[owner][path] = st
	if lm.byFile[path] == nil {
		lm.byFile[path] = make(map[*lockState]struct{})
	}
	lm.byFile[path][st] = struct{}{}
	return st
}

// findConflictLocked returns the first lock on path held by another owner
// that conflicts with the requested range.
func (lm *nfs4LockManager) findConflictLocked(path string, owner lockOwnerID, req lockRange) *lockDenied {
	for st := range lm.byFile[path] {
		if st.owner == owner {
			continue
		}
		for _, held := range st.ranges {
			if rangesConflict(held, req) {
				return rangeToDenied(held, st.owner)
			}
		}
	}
	return nil
}

// subtractRange removes [sub.start, sub.end) from the owner's ranges,
// splitting as needed (POSIX unlock/replace semantics).
func subtractRange(ranges []lockRange, sub lockRange) []lockRange {
	out := ranges[:0]
	for _, held := range ranges {
		if !held.overlaps(sub) {
			out = append(out, held)
			continue
		}
		if held.start < sub.start {
			out = append(out, lockRange{start: held.start, end: sub.start, lockType: held.lockType})
		}
		if sub.end < held.end {
			out = append(out, lockRange{start: sub.end, end: held.end, lockType: held.lockType})
		}
	}
	return out
}

// coalesce merges adjacent/overlapping same-type ranges to bound growth.
func coalesce(ranges []lockRange) []lockRange {
	if len(ranges) < 2 {
		return ranges
	}
	// Insertion sort: range lists are small (capped) and mostly sorted.
	for i := 1; i < len(ranges); i++ {
		for j := i; j > 0 && ranges[j].start < ranges[j-1].start; j-- {
			ranges[j], ranges[j-1] = ranges[j-1], ranges[j]
		}
	}
	out := ranges[:1]
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.lockType == last.lockType && r.start <= last.end {
			if r.end > last.end {
				last.end = r.end
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// lock acquires or upgrades a range for (owner, path). On success it returns
// the lock state (stateid seqid already advanced). On conflict it returns the
// conflicting lock.
func (lm *nfs4LockManager) lock(owner lockOwnerID, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.lockLocked(owner, path, req)
}

// lockByStateID acquires a range using an existing lock stateid.
func (lm *nfs4LockManager) lockByStateID(other [nfs4OtherSize]byte, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	st, ok := lm.states[other]
	if !ok || st.path != path {
		return nil, nil, nfs4ErrBadStateID
	}
	return lm.lockLocked(st.owner, path, req)
}

func (lm *nfs4LockManager) lockLocked(owner lockOwnerID, path string, req lockRange) (*lockState, *lockDenied, nfs4Status) {
	lm.touchClient(owner.clientID)

	if denied := lm.findConflictLocked(path, owner, req); denied != nil {
		return nil, denied, nfs4ErrDenied
	}

	st := lm.byOwner[owner][path]
	if st == nil {
		st = lm.newStateLocked(owner, path)
	}

	before := len(st.ranges)
	st.ranges = coalesce(append(subtractRange(st.ranges, req), req))
	delta := len(st.ranges) - before

	fileLocks := 0
	for other := range lm.byFile[path] {
		fileLocks += len(other.ranges)
	}
	if fileLocks > maxLocksPerFile || lm.clientLocks[owner.clientID]+delta > maxLocksPerClient {
		// Roll back: remove what we added, restore is not exact (the
		// replaced same-owner ranges are gone) but the owner asked to
		// overwrite them anyway; dropping the new range is safe.
		st.ranges = subtractRange(st.ranges, req)
		if len(st.ranges) == 0 {
			lm.dropStateLocked(st.other, st)
		}
		return nil, nil, nfs4ErrResource
	}
	lm.clientLocks[owner.clientID] += delta

	st.seqid++
	return st, nil, nfs4OK
}

// unlock releases a range held under a lock stateid.
func (lm *nfs4LockManager) unlock(other [nfs4OtherSize]byte, path string, req lockRange) (*lockState, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	st, ok := lm.states[other]
	if !ok || st.path != path {
		return nil, nfs4ErrBadStateID
	}
	lm.touchClient(st.owner.clientID)

	before := len(st.ranges)
	st.ranges = subtractRange(st.ranges, req)
	lm.clientLocks[st.owner.clientID] += len(st.ranges) - before
	if lm.clientLocks[st.owner.clientID] <= 0 {
		delete(lm.clientLocks, st.owner.clientID)
	}
	st.seqid++
	return st, nfs4OK
}

// test checks whether a range could be locked by owner (LOCKT).
func (lm *nfs4LockManager) test(owner lockOwnerID, path string, req lockRange) (*lockDenied, nfs4Status) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.touchClient(owner.clientID)

	if denied := lm.findConflictLocked(path, owner, req); denied != nil {
		return denied, nfs4ErrDenied
	}
	return nil, nfs4OK
}

// releaseOwner drops all lock state for an owner (RELEASE_LOCKOWNER).
func (lm *nfs4LockManager) releaseOwner(owner lockOwnerID) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.touchClient(owner.clientID)

	files := lm.byOwner[owner]
	for _, st := range files {
		lm.dropStateLocked(st.other, st)
	}
}

// lockStateID renders the 16-byte stateid for a lock state snapshot.
func lockStateID(seqid uint32, other [nfs4OtherSize]byte) []byte {
	stateID := make([]byte, 16)
	binary.BigEndian.PutUint32(stateID[0:4], seqid)
	copy(stateID[4:], other[:])
	return stateID
}

// --- Server plumbing ---

// lockManager returns the per-server lock manager, creating it on first use.
func (s *Server) lockManager() *nfs4LockManager {
	s.lockMgrOnce.Do(func() {
		s.lockMgr = newNFS4LockManager()
	})
	return s.lockMgr
}

// --- Operation handlers ---

func writeLockDenied(wr *nfs4Writer, denied *lockDenied) {
	wr.writeUint64(denied.offset)
	wr.writeUint64(denied.length)
	wr.writeUint32(denied.lockType)
	wr.writeUint64(denied.owner.clientID)
	wr.writeOpaque([]byte(denied.owner.owner))
}

// nfs4OpLock implements LOCK (RFC 7530 section 16.10).
func nfs4OpLock(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	reclaim, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	newLockOwner, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}

	var owner lockOwnerID
	var existingOther [nfs4OtherSize]byte
	haveExisting := false
	if newLockOwner != 0 {
		// open_to_lock_owner4
		if _, err := rd.readUint32(); err != nil { // open_seqid
			return nfs4ErrBadXDR
		}
		if _, err := rd.readFixedOpaque(16); err != nil { // open_stateid
			return nfs4ErrBadXDR
		}
		if _, err := rd.readUint32(); err != nil { // lock_seqid
			return nfs4ErrBadXDR
		}
		clientID, err := rd.readUint64()
		if err != nil {
			return nfs4ErrBadXDR
		}
		ownerBytes, err := rd.readOpaque(nfs4OpaqueLimit)
		if err != nil {
			return nfs4ErrBadXDR
		}
		owner = lockOwnerID{clientID: clientID, owner: string(ownerBytes)}
	} else {
		// exist_lock_owner4
		stateID, err := rd.readFixedOpaque(16)
		if err != nil {
			return nfs4ErrBadXDR
		}
		if _, err := rd.readUint32(); err != nil { // lock_seqid
			return nfs4ErrBadXDR
		}
		copy(existingOther[:], stateID[4:16])
		haveExisting = true
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	if reclaim != 0 {
		// No grace period: this server has no persistent lock state to
		// reclaim after restart.
		return nfs4ErrNoGrace
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	path := stringsJoinPath(current.path)
	lm := w.Server.lockManager()

	var st *lockState
	var denied *lockDenied
	if haveExisting {
		st, denied, status = lm.lockByStateID(existingOther, path, req)
	} else {
		st, denied, status = lm.lock(owner, path, req)
	}
	if status == nfs4ErrDenied {
		writeLockDenied(wr, denied)
		return nfs4ErrDenied
	}
	if status != nfs4OK {
		return status
	}

	wr.writeFixedOpaque(lockStateID(st.seqid, st.other))
	return nfs4OK
}

// nfs4OpLockT implements LOCKT (RFC 7530 section 16.11).
func nfs4OpLockT(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	clientID, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	ownerBytes, err := rd.readOpaque(nfs4OpaqueLimit)
	if err != nil {
		return nfs4ErrBadXDR
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	owner := lockOwnerID{clientID: clientID, owner: string(ownerBytes)}
	denied, status := w.Server.lockManager().test(owner, stringsJoinPath(current.path), req)
	if status == nfs4ErrDenied {
		writeLockDenied(wr, denied)
		return nfs4ErrDenied
	}
	return status
}

// nfs4OpLockU implements LOCKU (RFC 7530 section 16.12).
func nfs4OpLockU(rd *nfs4Reader, wr *nfs4Writer, w *response, state *nfs4CompoundState) nfs4Status {
	lockType, err := rd.readUint32()
	if err != nil {
		return nfs4ErrBadXDR
	}
	if _, err := rd.readUint32(); err != nil { // seqid
		return nfs4ErrBadXDR
	}
	stateID, err := rd.readFixedOpaque(16)
	if err != nil {
		return nfs4ErrBadXDR
	}
	offset, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}
	length, err := rd.readUint64()
	if err != nil {
		return nfs4ErrBadXDR
	}

	current, status := state.requireCurrent()
	if status != nfs4OK {
		return status
	}
	req, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		return status
	}

	var other [nfs4OtherSize]byte
	copy(other[:], stateID[4:16])
	st, status := w.Server.lockManager().unlock(other, stringsJoinPath(current.path), req)
	if status != nfs4OK {
		return status
	}

	wr.writeFixedOpaque(lockStateID(st.seqid, st.other))
	return nfs4OK
}

// Made with Bob
