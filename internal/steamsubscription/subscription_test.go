package steamsubscription

import (
	"context"
	"testing"
	"time"
)

type fakeClient struct {
	states          map[uint64]uint32
	requests        []uint64
	wanted          bool
	reject, stalled bool
}

func (f *fakeClient) State(id uint64) uint32 { return f.states[id] }
func (f *fakeClient) Subscribe(id uint64) bool {
	f.wanted = true
	f.requests = append(f.requests, id)
	return !f.reject
}
func (f *fakeClient) Unsubscribe(id uint64) bool {
	f.wanted = false
	f.requests = append(f.requests, id)
	return !f.reject
}
func (f *fakeClient) RunCallbacks() {
	if f.stalled || f.reject {
		return
	}
	for _, id := range f.requests {
		if f.wanted {
			f.states[id] |= 1
		} else {
			f.states[id] &^= 1
		}
	}
}
func TestSubscribeAndUnsubscribeExactlyRequestedItems(t *testing.T) {
	if SkeetoItems != [2]uint64{3782512354, 3787239937} {
		t.Fatal(SkeetoItems)
	}
	f := &fakeClient{states: map[uint64]uint32{42: 1}}
	for _, wanted := range []bool{true, false} {
		f.requests = nil
		if e := Set(context.Background(), f, SkeetoItems[:], wanted, time.Millisecond); e != nil {
			t.Fatal(e)
		}
		if len(f.requests) != 2 || f.states[42] != 1 {
			t.Fatal("unrelated subscription changed", f)
		}
		for _, id := range SkeetoItems {
			if (f.states[id]&1 != 0) != wanted {
				t.Fatal("unconfirmed", id)
			}
		}
	}
}
func TestIdempotentAndPartial(t *testing.T) {
	f := &fakeClient{states: map[uint64]uint32{SkeetoItems[0]: 1}}
	if e := Subscribe(context.Background(), f, SkeetoItems[:], time.Millisecond); e != nil {
		t.Fatal(e)
	}
	if len(f.requests) != 1 || f.requests[0] != SkeetoItems[1] {
		t.Fatal(f.requests)
	}
	f.requests = nil
	if e := Subscribe(context.Background(), f, SkeetoItems[:], time.Millisecond); e != nil || len(f.requests) != 0 {
		t.Fatal(e, f.requests)
	}
}
func TestFailureNeverReportedAsConfirmed(t *testing.T) {
	for _, wanted := range []bool{true, false} {
		for _, reject := range []bool{true, false} {
			f := &fakeClient{states: map[uint64]uint32{}, reject: reject, stalled: true}
			if !wanted {
				for _, id := range SkeetoItems {
					f.states[id] = 1
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Millisecond)
			e := Set(ctx, f, SkeetoItems[:], wanted, time.Millisecond)
			cancel()
			if e == nil {
				t.Fatal("unconfirmed state accepted")
			}
		}
	}
}
func TestCanceledDoesNotChangeSubscriptions(t *testing.T) {
	f := &fakeClient{states: map[uint64]uint32{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Set(ctx, f, SkeetoItems[:], true, time.Millisecond); e == nil || len(f.requests) != 0 {
		t.Fatal(e, f.requests)
	}
}
