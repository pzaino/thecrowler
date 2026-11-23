package vdi

import (
	"errors"
	"testing"
	"time"
)

type fakeQuitter struct {
	currentURLErr error
	closeErr      error
	quitErr       error
	currentCalls  int
	closeCalls    int
	quitCalls     int
}

func (f *fakeQuitter) CurrentURL() (string, error) {
	f.currentCalls++
	return "", f.currentURLErr
}

func (f *fakeQuitter) Close() error {
	f.closeCalls++
	return f.closeErr
}

func (f *fakeQuitter) Quit() error {
	f.quitCalls++
	return f.quitErr
}

func TestQuitWebDriverSessionStillAttemptsCloseAndQuitAfterInvalidProbe(t *testing.T) {
	fake := &fakeQuitter{currentURLErr: errors.New("invalid session id")}
	quitWebDriverSession(fake, func(time.Duration) {})
	if fake.currentCalls != 1 || fake.closeCalls != 1 || fake.quitCalls != 1 {
		t.Fatalf("calls = current:%d close:%d quit:%d, want 1/1/1",
			fake.currentCalls, fake.closeCalls, fake.quitCalls)
	}
}

func TestQuitWebDriverSessionStillAttemptsQuitAfterCloseFailure(t *testing.T) {
	fake := &fakeQuitter{closeErr: errors.New("close failed")}
	quitWebDriverSession(fake, func(time.Duration) {})
	if fake.closeCalls != 1 || fake.quitCalls != 1 {
		t.Fatalf("calls = close:%d quit:%d, want 1/1", fake.closeCalls, fake.quitCalls)
	}
}

func TestQuitWebDriverSessionNormalPath(t *testing.T) {
	fake := &fakeQuitter{}
	var settled bool
	quitWebDriverSession(fake, func(time.Duration) { settled = true })
	if fake.currentCalls != 1 || fake.closeCalls != 1 || fake.quitCalls != 1 {
		t.Fatalf("calls = current:%d close:%d quit:%d, want 1/1/1",
			fake.currentCalls, fake.closeCalls, fake.quitCalls)
	}
	if !settled {
		t.Fatal("close settle hook was not called on a successful Close")
	}
}

func TestQuitWebDriverSessionNilIsSafe(t *testing.T) {
	quitWebDriverSession(nil, func(time.Duration) {})
}
