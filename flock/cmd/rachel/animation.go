package main

import (
	"mazzy/mazarin/uring"
	"mazzy/shared/wm"
	"sort"
)

// activeAnimation tracks a registered animation in rachel's sorted list.
type activeAnimation struct {
	id         uint64
	targetSID  int
	startNanos int64
	endNanos   int64
	started    bool // true after AnimationStart sent
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
			AnimationID:  anim.id,
			StartNanos:   anim.startNanos,
			EndNanos:     anim.endNanos,
			CoveredStart: cs,
			CoveredEnd:   ce,
		})
		_ = uring.Send(anim.targetSID, &updateMsg)
		i++
	}
}
