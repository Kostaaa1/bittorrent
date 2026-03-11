package peer

import (
	"context"
	"time"
)

const chokeTimeout = time.Second * 60   // resets on CHOKE
const requestTimeout = time.Second * 20 // resets on PIECE
const keepAliveTimeout = time.Minute * 2

type eventType int

const (
	eventAdd eventType = iota
	eventReset
	eventCancel
)

type eventMessage struct {
	msg        messageID
	eventType  eventType
	maxTimeout time.Duration
}

type event struct {
	occurred   time.Time
	maxTimeout time.Duration
}

func (event *event) exceeded() bool {
	return time.Since(event.occurred) > event.maxTimeout
}

type timeoutManager struct {
	ch         chan eventMessage
	ExceedChan chan messageID
}

func newTimeoutManager() *timeoutManager {
	return &timeoutManager{
		ch: make(chan eventMessage),
	}
}

func (t *timeoutManager) add(msg messageID, timeout time.Duration) {
	t.ch <- eventMessage{
		msg:        msg,
		eventType:  eventAdd,
		maxTimeout: timeout,
	}
}

func (t *timeoutManager) cancel(msg messageID) {
	t.ch <- eventMessage{
		msg:       msg,
		eventType: eventCancel,
	}
}

func (t *timeoutManager) reset(msg messageID) {
	t.ch <- eventMessage{
		msg:       msg,
		eventType: eventReset,
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
		case msg := <-t.ch:
			ev := events[msg.msg]

			switch msg.eventType {
			case eventCancel:
				delete(events, messageID(msg.msg))
			case eventAdd:
				if _, ok := events[msg.msg]; !ok {
					events[msg.msg] = &event{
						occurred:   time.Now(),
						maxTimeout: msg.maxTimeout,
					}
				}
			case eventReset:
				ev.occurred = time.Now()
			}
		case <-ticker.C:
			for name, ev := range events {
				if time.Since(ev.occurred) > ev.maxTimeout {
					t.ExceedChan <- name
					delete(events, name)
				}
			}
		}
	}
}
