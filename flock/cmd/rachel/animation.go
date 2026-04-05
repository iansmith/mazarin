package main

import (
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	"sort"
)

// Key repeat timing constants.
const (
	keyRepeatDelayNs    int64 = 500_000_000  // M: initial delay before repeat starts
	keyRepeatIntervalNs int64 = 175_000_000  // N: interval between repeated keystrokes
	keyRepeatFastAfterNs int64 = 1_500_000_000 // after this elapsed, switch to fast rate
	keyRepeatFastNs     int64 = 100_000_000  // fast rate interval
)

// keyRepeatInfo marks an animation as a key-repeat animation. Instead of
// sending AnimationUpdate messages, the tick handler sends KeyPress messages
// to the target SID at the configured repeat rate.
type keyRepeatInfo struct {
	msg             wm.KeyPress // the KeyPress to re-emit
	repeating       bool        // true once initial delay (M) has elapsed
	lastRepeatNanos int64       // absolute nanos of last repeat emission
}

// keyRepeatByCode maps evdev keycode → animation ID so that KeyUp can
// find and unregister the corresponding repeat animation.
var keyRepeatByCode = make(map[uint16]uint64)

// activeAnimation tracks a registered animation in rachel's sorted list.
type activeAnimation struct {
	id         uint64
	targetSID  int
	startNanos int64
	endNanos   int64
	started    bool // true after AnimationStart sent
	keyRepeat  *keyRepeatInfo
}

// animations is kept sorted by startNanos (ascending).
var animations []activeAnimation
var nextAnimID uint64 = 1

// registerAnimation handles an AnimationRegister message from a shepherd.
func registerAnimation(senderSID int, msg wm.AnimationRegister) {
	id := nextAnimID
	nextAnimID++

	// Send AnimationRegistered back to the sender.
	regMsg := wm.EncodeAnimationRegistered(&wm.AnimationRegistered{
		AnimationID: id,
		Nonce:       msg.Nonce,
	})
	_ = uring.Send(senderSID, &regMsg)

	// Insert into sorted list by startNanos.
	anim := activeAnimation{
		id:         id,
		targetSID:  senderSID,
		startNanos: msg.StartNanos,
		endNanos:   msg.EndNanos,
	}
	idx := sort.Search(len(animations), func(i int) bool {
		return animations[i].startNanos > msg.StartNanos
	})
	animations = append(animations, activeAnimation{})
	copy(animations[idx+1:], animations[idx:])
	animations[idx] = anim
}

// unregisterAnimation handles an AnimationUnregister message from a shepherd.
// Removes the animation from the list and sends AnimationUnregistered back.
func unregisterAnimation(senderSID int, msg wm.AnimationUnregister) {
	// Find and remove from sorted list.
	for i, anim := range animations {
		if anim.id == msg.AnimationID {
			animations = append(animations[:i], animations[i+1:]...)
			break
		}
	}
	// Send confirmation back to the sender.
	reply := wm.EncodeAnimationUnregistered(&wm.AnimationUnregistered{
		AnimationID: msg.AnimationID,
		Nonce:       msg.Nonce,
	})
	_ = uring.Send(senderSID, &reply)
}

// startKeyRepeat registers an AnimateAlways animation for key auto-repeat.
// Called from WindowInteractor.KeyDown. Returns the animation ID.
func startKeyRepeat(targetSID int, nowNanos int64, kp wm.KeyPress) uint64 {
	// If there's already a repeat for this keycode, remove it first.
	if oldID, ok := keyRepeatByCode[kp.Code]; ok {
		for i, anim := range animations {
			if anim.id == oldID {
				animations = append(animations[:i], animations[i+1:]...)
				break
			}
		}
		delete(keyRepeatByCode, kp.Code)
	}

	id := nextAnimID
	nextAnimID++

	anim := activeAnimation{
		id:         id,
		targetSID:  targetSID,
		startNanos: nowNanos,
		endNanos:   wm.AnimationAlways,
		keyRepeat: &keyRepeatInfo{
			msg: kp,
		},
	}
	idx := sort.Search(len(animations), func(i int) bool {
		return animations[i].startNanos > nowNanos
	})
	animations = append(animations, activeAnimation{})
	copy(animations[idx+1:], animations[idx:])
	animations[idx] = anim

	keyRepeatByCode[kp.Code] = id
	return id
}

// stopKeyRepeat removes the key-repeat animation for the given keycode.
// Called from WindowInteractor.KeyUp.
func stopKeyRepeat(keyCode uint16) {
	animID, ok := keyRepeatByCode[keyCode]
	if !ok {
		return
	}
	delete(keyRepeatByCode, keyCode)
	for i, anim := range animations {
		if anim.id == animID {
			animations = append(animations[:i], animations[i+1:]...)
			return
		}
	}
}

// tickAnimations walks the animation list for the current interval
// [intervalStartVal, now) and sends Start/Update/Finish messages.
func tickAnimations(intervalStartVal, now int64) {
	i := 0
	for i < len(animations) {
		anim := &animations[i]

		if anim.endNanos <= now {
			// Past end — send finish (and start if never sent).
			if !anim.started {
				startMsg := wm.EncodeAnimationStart(&wm.AnimationStart{
					AnimationID: anim.id,
					StartNanos:  anim.startNanos,
				})
				_ = uring.Send(anim.targetSID, &startMsg)
			}
			finishMsg := wm.EncodeAnimationFinish(&wm.AnimationFinish{
				AnimationID: anim.id,
				EndNanos:    anim.endNanos,
			})
			_ = uring.Send(anim.targetSID, &finishMsg)
			// Remove from list.
			animations = append(animations[:i], animations[i+1:]...)
			continue
		}

		if anim.startNanos > now {
			break // remaining animations are in the future
		}

		// Active: startNanos <= now < endNanos

		// Key-repeat animations are rachel-internal: no Start/Update/Finish
		// messages to the shepherd — just KeyPress emissions.
		if anim.keyRepeat != nil {
			anim.started = true
			tickKeyRepeat(anim, now)
			i++
			continue
		}

		if !anim.started {
			startMsg := wm.EncodeAnimationStart(&wm.AnimationStart{
				AnimationID: anim.id,
				StartNanos:  anim.startNanos,
			})
			_ = uring.Send(anim.targetSID, &startMsg)
			anim.started = true
		}

		duration := float64(anim.endNanos - anim.startNanos)
		cs := float64(intervalStartVal-anim.startNanos) / duration
		ce := float64(now-anim.startNanos) / duration
		if cs < 0 {
			cs = 0
		}
		if cs > 1 {
			cs = 1
		}
		if ce < 0 {
			ce = 0
		}
		if ce > 1 {
			ce = 1
		}

		updateMsg := wm.EncodeAnimationUpdate(&wm.AnimationUpdate{
			AnimationID:     anim.id,
			StartNanos:      anim.startNanos,
			EndNanos:        anim.endNanos,
			CoveredStart:    cs,
			CoveredEnd:      ce,
			NanosSinceStart: now - anim.startNanos,
		})
		_ = uring.Send(anim.targetSID, &updateMsg)
		i++
	}
}

// tickKeyRepeat handles one animation tick for a key-repeat animation.
// It checks elapsed time since animation start and emits KeyPress messages
// according to the delay (M) and interval (N) schedule.
//
// If the first tick arrives after both M and some N intervals have passed
// (e.g., 800ms = past 500ms delay + one 250ms interval), all due repeats
// are emitted in a single tick.
func tickKeyRepeat(anim *activeAnimation, now int64) {
	kr := anim.keyRepeat
	elapsed := now - anim.startNanos

	if elapsed < keyRepeatDelayNs {
		// Still in the initial delay period — nothing to do.
		return
	}

	if !kr.repeating {
		// Just crossed the M threshold. Switch to repeat mode and
		// set the baseline for interval counting.
		kr.repeating = true
		kr.lastRepeatNanos = anim.startNanos + keyRepeatDelayNs
	}

	// Pick the interval based on how long the key has been held.
	interval := keyRepeatIntervalNs
	if elapsed >= keyRepeatFastAfterNs {
		interval = keyRepeatFastNs
	}

	// Emit one KeyPress for each interval that has elapsed since
	// the last repeat emission. If the rate just changed, the shorter
	// interval naturally catches up by emitting more events.
	for kr.lastRepeatNanos+interval <= now {
		kr.lastRepeatNanos += interval
		msg := wm.EncodeKeyPress(&kr.msg)
		_ = uring.Send(anim.targetSID, &msg)
	}
}
