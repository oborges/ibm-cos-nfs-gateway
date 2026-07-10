package nfs

import (
	"bytes"
	"testing"
	"time"
)

func mustRange(t *testing.T, offset, length uint64, lockType uint32) lockRange {
	t.Helper()
	r, status := makeRange(offset, length, lockType)
	if status != nfs4OK {
		t.Fatalf("makeRange(%d,%d,%d) status = %d", offset, length, lockType, status)
	}
	return r
}

func TestMakeRangeValidation(t *testing.T) {
	if _, status := makeRange(0, 0, writeLT); status != nfs4ErrInval {
		t.Errorf("zero length: status = %d, want INVAL", status)
	}
	if _, status := makeRange(10, ^uint64(0)-5, writeLT); status != nfs4ErrInval {
		t.Errorf("overflowing range: status = %d, want INVAL", status)
	}
	if _, status := makeRange(0, 100, 9); status != nfs4ErrInval {
		t.Errorf("bad lock type: status = %d, want INVAL", status)
	}
	r := mustRange(t, 5, nfs4LengthEOF, readWLT)
	if r.end != nfs4LengthEOF || r.lockType != readLT {
		t.Errorf("EOF blocking read = %+v, want end EOF, type READ", r)
	}
}

func TestLockConflictsBetweenOwners(t *testing.T) {
	lm := newNFS4LockManager()
	alice := lockOwnerID{clientID: 1, owner: "alice"}
	bob := lockOwnerID{clientID: 2, owner: "bob"}

	if _, denied, status := lm.lock(alice, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4OK || denied != nil {
		t.Fatalf("alice write lock: status = %d", status)
	}

	// Overlapping write from another owner is denied with conflict info.
	_, denied, status := lm.lock(bob, "/f", mustRange(t, 50, 100, writeLT))
	if status != nfs4ErrDenied || denied == nil {
		t.Fatalf("bob overlapping write: status = %d denied = %v, want DENIED", status, denied)
	}
	if denied.owner != alice || denied.offset != 0 || denied.length != 100 || denied.lockType != writeLT {
		t.Fatalf("denied = %+v, want alice's 0-100 write lock", denied)
	}

	// Read vs write conflicts; read vs read does not.
	if _, _, status := lm.lock(bob, "/f", mustRange(t, 0, 10, readLT)); status != nfs4ErrDenied {
		t.Fatalf("bob read over alice write: status = %d, want DENIED", status)
	}
	if _, _, status := lm.lock(bob, "/f", mustRange(t, 200, 100, writeLT)); status != nfs4OK {
		t.Fatalf("bob disjoint write: status = %d, want OK", status)
	}
	if _, _, status := lm.lock(alice, "/g", mustRange(t, 0, 10, readLT)); status != nfs4OK {
		t.Fatalf("alice read on /g: status = %d", status)
	}
	if _, _, status := lm.lock(bob, "/g", mustRange(t, 0, 10, readLT)); status != nfs4OK {
		t.Fatalf("bob shared read on /g: status = %d, want OK (read locks share)", status)
	}
}

func TestSameOwnerReplaceAndUnlockSplit(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}

	st, _, status := lm.lock(owner, "/f", mustRange(t, 0, 100, writeLT))
	if status != nfs4OK {
		t.Fatalf("initial lock: status = %d", status)
	}
	// Same owner downgrades the middle to a read lock: replaces the overlap.
	st2, _, status := lm.lock(owner, "/f", mustRange(t, 25, 50, readLT))
	if status != nfs4OK {
		t.Fatalf("downgrade middle: status = %d", status)
	}
	if st2 != st {
		t.Fatal("same owner+file should reuse the lock state")
	}
	if len(st.ranges) != 3 {
		t.Fatalf("ranges = %+v, want write/read/write split into 3", st.ranges)
	}

	// Unlock the middle: the read range disappears, writes remain.
	if _, status := lm.unlock(st.other, "/f", mustRange(t, 25, 50, writeLT)); status != nfs4OK {
		t.Fatalf("unlock middle: status = %d", status)
	}
	if len(st.ranges) != 2 {
		t.Fatalf("ranges after unlock = %+v, want 2", st.ranges)
	}

	// Another owner can now lock the freed middle.
	other := lockOwnerID{clientID: 2, owner: "p"}
	if _, _, status := lm.lock(other, "/f", mustRange(t, 30, 10, writeLT)); status != nfs4OK {
		t.Fatalf("other owner locking freed middle: status = %d, want OK", status)
	}
}

func TestUnlockBadStateID(t *testing.T) {
	lm := newNFS4LockManager()
	var bogus [nfs4OtherSize]byte
	if _, status := lm.unlock(bogus, "/f", lockRange{start: 0, end: 10, lockType: writeLT}); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with unknown stateid: status = %d, want BAD_STATEID", status)
	}

	owner := lockOwnerID{clientID: 1, owner: "o"}
	st, _, _ := lm.lock(owner, "/f", lockRange{start: 0, end: 10, lockType: writeLT})
	if _, status := lm.unlock(st.other, "/WRONG", lockRange{start: 0, end: 10, lockType: writeLT}); status != nfs4ErrBadStateID {
		t.Fatalf("unlock with wrong path: status = %d, want BAD_STATEID", status)
	}
}

func TestLockByStateID(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}
	st, _, _ := lm.lock(owner, "/f", lockRange{start: 0, end: 10, lockType: writeLT})
	seqBefore := st.seqid

	st2, _, status := lm.lockByStateID(st.other, "/f", lockRange{start: 20, end: 30, lockType: writeLT})
	if status != nfs4OK || st2 != st {
		t.Fatalf("lockByStateID: status = %d", status)
	}
	if st.seqid != seqBefore+1 {
		t.Fatalf("stateid seqid = %d, want %d (must advance)", st.seqid, seqBefore+1)
	}
	if len(st.ranges) != 2 {
		t.Fatalf("ranges = %+v, want 2 disjoint", st.ranges)
	}
}

func TestLockTExcludesRequestingOwner(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}
	if _, _, status := lm.lock(owner, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4OK {
		t.Fatal("setup lock failed")
	}

	// The holding owner's own test must not conflict.
	if denied, status := lm.test(owner, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4OK || denied != nil {
		t.Fatalf("self test: status = %d, want OK", status)
	}
	// Another owner sees the conflict.
	if _, status := lm.test(lockOwnerID{clientID: 2, owner: "p"}, "/f", mustRange(t, 0, 100, writeLT)); status != nfs4ErrDenied {
		t.Fatalf("foreign test: status = %d, want DENIED", status)
	}
}

func TestReleaseOwnerFreesLocks(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}
	lm.lock(owner, "/f", lockRange{start: 0, end: 100, lockType: writeLT})
	lm.lock(owner, "/g", lockRange{start: 0, end: 100, lockType: writeLT})

	lm.releaseOwner(owner)

	other := lockOwnerID{clientID: 2, owner: "p"}
	if _, _, status := lm.lock(other, "/f", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4OK {
		t.Fatalf("lock after release: status = %d, want OK", status)
	}
	if _, _, status := lm.lock(other, "/g", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4OK {
		t.Fatalf("lock after release on /g: status = %d, want OK", status)
	}
	if len(lm.states) != 2 {
		t.Fatalf("states = %d, want only the new owner's 2", len(lm.states))
	}
}

func TestPerFileLockCap(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}

	// Disjoint 1-byte locks with gaps cannot coalesce.
	for i := 0; i < maxLocksPerFile; i++ {
		if _, _, status := lm.lock(owner, "/f", lockRange{start: uint64(i * 2), end: uint64(i*2 + 1), lockType: writeLT}); status != nfs4OK {
			t.Fatalf("lock %d: status = %d", i, status)
		}
	}
	if _, _, status := lm.lock(owner, "/f", lockRange{start: 100000, end: 100001, lockType: writeLT}); status != nfs4ErrResource {
		t.Fatalf("lock beyond per-file cap: status = %d, want RESOURCE", status)
	}
	// Another file is unaffected.
	if _, _, status := lm.lock(owner, "/g", lockRange{start: 0, end: 1, lockType: writeLT}); status != nfs4OK {
		t.Fatalf("lock on other file: status = %d, want OK", status)
	}
}

func TestLeaseExpiryDropsAbandonedLocks(t *testing.T) {
	lm := newNFS4LockManager()
	current := time.Unix(1000, 0)
	lm.now = func() time.Time { return current }

	dead := lockOwnerID{clientID: 1, owner: "dead"}
	if _, _, status := lm.lock(dead, "/f", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4OK {
		t.Fatal("setup lock failed")
	}

	// A live client's activity after the grace window expires the dead one.
	current = current.Add(lockLeaseGracePeriods*nfs4LeaseTimeSecs*time.Second + time.Second)
	live := lockOwnerID{clientID: 2, owner: "live"}
	if _, _, status := lm.lock(live, "/f", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4OK {
		t.Fatalf("live lock after dead lease expiry: status = %d, want OK", status)
	}
	if _, seen := lm.clientSeen[dead.clientID]; seen {
		t.Fatal("dead client lease record should be gone")
	}
}

func TestRenewKeepsLeaseAlive(t *testing.T) {
	lm := newNFS4LockManager()
	current := time.Unix(1000, 0)
	lm.now = func() time.Time { return current }

	holder := lockOwnerID{clientID: 1, owner: "holder"}
	if _, _, status := lm.lock(holder, "/f", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4OK {
		t.Fatal("setup lock failed")
	}

	// Renew inside the window repeatedly; the lock must survive well past
	// the original grace deadline.
	for i := 0; i < 10; i++ {
		current = current.Add(nfs4LeaseTimeSecs * time.Second)
		lm.renewClient(holder.clientID)
	}
	other := lockOwnerID{clientID: 2, owner: "other"}
	if _, status := lm.test(other, "/f", lockRange{start: 0, end: 100, lockType: writeLT}); status != nfs4ErrDenied {
		t.Fatalf("test after renewals: status = %d, want DENIED (lock still held)", status)
	}
}

func TestCoalesceMergesSameTypeRanges(t *testing.T) {
	lm := newNFS4LockManager()
	owner := lockOwnerID{clientID: 1, owner: "o"}

	// Adjacent same-type locks merge into one range.
	lm.lock(owner, "/f", lockRange{start: 0, end: 10, lockType: writeLT})
	lm.lock(owner, "/f", lockRange{start: 10, end: 20, lockType: writeLT})
	st, _, _ := lm.lock(owner, "/f", lockRange{start: 20, end: 30, lockType: writeLT})
	if len(st.ranges) != 1 || st.ranges[0].start != 0 || st.ranges[0].end != 30 {
		t.Fatalf("ranges = %+v, want single [0,30)", st.ranges)
	}
}

func TestCompoundResponseKeepsDeniedLockBody(t *testing.T) {
	// LOCK DENIED results must carry the LOCK4denied payload; clients fail
	// to decode the response without it and retry until the conflict
	// disappears, which reads as a granted lock. (Found via cross-client
	// locking between two real NFS client machines.)
	var deniedBody bytes.Buffer
	wr := newNFS4Writer(&deniedBody)
	writeLockDenied(wr, &lockDenied{offset: 0, length: 100, lockType: writeLT,
		owner: lockOwnerID{clientID: 0xABCD, owner: "conflicting-owner"}})

	results := []nfs4Result{
		{op: opPutFH, status: nfs4OK},
		{op: opLock, status: nfs4ErrDenied, body: deniedBody.Bytes()},
	}

	var out bytes.Buffer
	w := &response{writer: &out, req: &request{xid: 7}}
	if err := writeNFSv4CompoundResponse(w, nfs4ErrDenied, nil, results); err != nil {
		t.Fatalf("writeNFSv4CompoundResponse() error = %v", err)
	}

	if !bytes.Contains(out.Bytes(), deniedBody.Bytes()) {
		t.Fatal("compound response must include the LOCK4denied body for DENIED lock ops")
	}
}

// Made with Bob
