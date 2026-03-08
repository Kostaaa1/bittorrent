package peer

import (
	"context"
	"time"
)

const chokeTimeout = time.Second * 45   // resets on CHOKE
const requestTimeout = time.Second * 20 // resets on PIECE
const keepAliveTimeout = time.Minute * 2

type eventType int

const (
	eventAdd eventType = iota
	eventReset
	eventDelete
)

type eventMessage struct {
	msg        messageID
	eventType  eventType
	maxTimeout time.Duration
	onExceed   func()
}

type event struct {
	occurred   time.Time
	maxTimeout time.Duration
	onExceed   func()
}

func (event *event) exceeded() bool {
	return time.Since(event.occurred) > event.maxTimeout
}

type timeoutManager struct {
	C chan eventMessage
}

func newTimeoutManager() *timeoutManager {
	return &timeoutManager{
		C: make(chan eventMessage),
	}
}

func (t *timeoutManager) run(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	events := make(map[messageID]*event)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-t.C:
			ev := events[msg.msg]

			switch msg.eventType {
			case eventDelete:
				delete(events, messageID(msg.eventType))
			case eventAdd:
				if _, ok := events[msg.msg]; !ok {
					events[msg.msg] = &event{
						occurred:   time.Now(),
						maxTimeout: msg.maxTimeout,
						onExceed:   msg.onExceed,
					}
				}
			case eventReset:
				ev.occurred = time.Now()
			}
		case <-ticker.C:
			for name, event := range events {
				if event.exceeded() {
					event.onExceed()
					delete(events, name)
				}
			}
		}
	}
}
