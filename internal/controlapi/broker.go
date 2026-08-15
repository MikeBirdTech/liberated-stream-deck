package controlapi

import "sync"

const subscriberBuffer = 256

// eventBroker fan-outs input events without ever blocking the HID reader. A
// subscriber that cannot keep up is disconnected rather than silently missing
// a key release or dial transition.
type eventBroker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
}

func newEventBroker() *eventBroker {
	return &eventBroker{subscribers: make(map[uint64]chan Event)}
}

func (b *eventBroker) subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, subscriberBuffer)
	b.subscribers[id] = ch
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if current, ok := b.subscribers[id]; ok {
				delete(b.subscribers, id)
				close(current)
			}
			b.mu.Unlock()
		})
	}
}

func (b *eventBroker) publish(event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			delete(b.subscribers, id)
			close(ch)
		}
	}
}
