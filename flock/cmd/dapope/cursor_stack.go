package main

import (
	"mazzy/mazarin/gui/core"
	"sync"
)

type cursorStackDefault struct {
	mu     sync.Mutex
	bottom core.Cursor
	stack  []core.Cursor
	x, y   int // cursor position in quadrant-1 screen coords
}

func NewCursorStackDefault() core.CursorStack {
	return &cursorStackDefault{}
}

func (s *cursorStackDefault) Push(c core.Cursor) core.Cursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.currentLocked()
	s.stack = append(s.stack, c)
	return prev
}

func (s *cursorStackDefault) Pop() core.Cursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.stack)
	if n == 0 {
		return core.Cursor{}
	}
	top := s.stack[n-1]
	s.stack = s.stack[:n-1]
	return top
}

func (s *cursorStackDefault) Bottom(c core.Cursor) {
	s.mu.Lock()
	s.bottom = c
	s.mu.Unlock()
}

func (s *cursorStackDefault) Current() core.Cursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLocked()
}

func (s *cursorStackDefault) currentLocked() core.Cursor {
	if n := len(s.stack); n > 0 {
		return s.stack[n-1]
	}
	return s.bottom
}

func (s *cursorStackDefault) SetPosition(x, y int) {
	s.mu.Lock()
	s.x = clamp(x, 0, 1919)
	s.y = clamp(y, 0, 1079)
	s.mu.Unlock()
}

func (s *cursorStackDefault) Position() (int, int) {
	s.mu.Lock()
	x, y := s.x, s.y
	s.mu.Unlock()
	return x, y
}

func (s *cursorStackDefault) Move(dx, dy int) {
	s.mu.Lock()
	s.x = clamp(s.x+dx, 0, 1919)
	s.y = clamp(s.y+dy, 0, 1079)
	s.mu.Unlock()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
