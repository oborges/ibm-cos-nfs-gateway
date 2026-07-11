package cos

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	awsrequest "github.com/IBM/ibm-cos-sdk-go/aws/request"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newBreakerRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, "https://cos.example.test/bucket", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestBreakerOpensAndFastFailsAfterTransportFailures(t *testing.T) {
	var calls atomic.Int32
	backendErr := errors.New("dial failed")
	b := newBreakerTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, backendErr
	}))

	for i := 0; i < breakerFailureThreshold; i++ {
		_, err := b.RoundTrip(newBreakerRequest(t))
		if !errors.Is(err, backendErr) {
			t.Fatalf("failure %d error = %v, want backend error", i+1, err)
		}
	}
	if !b.Open() {
		t.Fatal("breaker remained closed after threshold failures")
	}

	start := time.Now()
	_, err := b.RoundTrip(newBreakerRequest(t))
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("open breaker error = %v, want ErrCircuitOpen", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("open breaker took %v, want immediate failure", elapsed)
	}
	if got := calls.Load(); got != breakerFailureThreshold {
		t.Fatalf("inner transport calls = %d, want %d", got, breakerFailureThreshold)
	}
}

func TestBreakerAnyHTTPResponseProvesReachability(t *testing.T) {
	b := newBreakerTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable}, nil
	}))
	b.open = true
	b.consecutiveFails = breakerFailureThreshold
	b.lastProbe = time.Now().Add(-breakerProbeInterval)

	resp, err := b.RoundTrip(newBreakerRequest(t))
	if err != nil {
		t.Fatalf("probe error = %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("probe status = %d, want 503", resp.StatusCode)
	}
	if b.Open() {
		t.Fatal("breaker remained open after an HTTP response")
	}
}

func TestBreakerAllowsOnlyOneRecoveryProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	b := newBreakerTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		return &http.Response{StatusCode: http.StatusOK}, nil
	}))
	b.open = true
	b.lastProbe = time.Now().Add(-breakerProbeInterval)

	done := make(chan error)
	probeReq := newBreakerRequest(t)
	go func() {
		_, err := b.RoundTrip(probeReq)
		done <- err
	}()
	<-started

	for i := 0; i < 10; i++ {
		_, err := b.RoundTrip(newBreakerRequest(t))
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("request %d error = %v, want ErrCircuitOpen", i, err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("recovery probe error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery probe calls = %d, want 1", got)
	}
}

func TestBreakerDoesNotCountExplicitCancellation(t *testing.T) {
	b := newBreakerTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	}))

	for i := 0; i < breakerFailureThreshold+1; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := newBreakerRequest(t).WithContext(ctx)
		_, _ = b.RoundTrip(req)
	}
	if b.Open() {
		t.Fatal("breaker opened because callers canceled their requests")
	}
}

func TestCircuitOpenIsNotSDKRetryable(t *testing.T) {
	err := awserr.New(
		awsrequest.ErrCodeRequestError,
		"send failed",
		&url.Error{Op: "Head", URL: "https://cos.example.test", Err: ErrCircuitOpen},
	)
	if awsrequest.IsErrorRetryable(err) {
		t.Fatal("SDK considers ErrCircuitOpen retryable; fast-fail would incur retry backoff")
	}
	if !isCircuitOpenError(err) {
		t.Fatal("isCircuitOpenError did not follow the SDK error chain")
	}
}
