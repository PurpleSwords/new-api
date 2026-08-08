package controller

import (
	"sync"
	"testing"
	"time"
)

func channelTestProbeLockExists(channelID int) bool {
	channelTestProbeLocksMu.Lock()
	defer channelTestProbeLocksMu.Unlock()
	_, ok := channelTestProbeLocks[channelID]
	return ok
}

func TestLockChannelTestProbeSerializesSameChannel(t *testing.T) {
	firstAcquired := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondAcquired := make(chan struct{})
	firstDone := make(chan struct{})

	unlock1 := lockChannelTestProbe(1)
	go func() {
		close(firstAcquired)
		<-releaseFirst
		unlock1()
		close(firstDone)
	}()
	<-firstAcquired

	go func() {
		unlock2 := lockChannelTestProbe(1)
		close(secondAcquired)
		unlock2()
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second probe acquired the same channel lock before the first released it")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirst)
	<-firstDone

	select {
	case <-secondAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second probe did not acquire the lock after the first released it")
	}
}

func TestLockChannelTestProbeAllowsDifferentChannels(t *testing.T) {
	unlock1 := lockChannelTestProbe(1)
	defer unlock1()

	done := make(chan struct{})
	go func() {
		unlock2 := lockChannelTestProbe(2)
		unlock2()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("different channel probes must not block each other")
	}
}

func TestLockChannelTestProbeRemovesEntryAfterRelease(t *testing.T) {
	unlock := lockChannelTestProbe(99)
	if !channelTestProbeLockExists(99) {
		t.Fatal("lock entry should exist while held")
	}
	unlock()
	if channelTestProbeLockExists(99) {
		t.Fatal("lock entry should be removed after the last holder releases it")
	}
}

func TestLockChannelTestProbeStressSameChannel(t *testing.T) {
	const goroutines = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			unlock := lockChannelTestProbe(123)
			unlock()
		}()
	}
	close(start)
	wg.Wait()
	if channelTestProbeLockExists(123) {
		t.Fatal("lock entry should be removed after all holders release it")
	}
}
