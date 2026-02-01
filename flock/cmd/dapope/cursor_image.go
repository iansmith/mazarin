package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"mazzy/mazarin/gui/core"
	"os"
	"sync"
)

type cursorImageDefault struct {
	mu     sync.Mutex
	images map[int32]core.Cursor
	nextID int32
}

func NewCursorImageDefault() core.CursorImageMap {
	return &cursorImageDefault{
		images: make(map[int32]core.Cursor),
		nextID: 1,
	}
}

func (m *cursorImageDefault) CursorImageAdd(img image.Image) core.Cursor {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		b := img.Bounds()
		rgba = image.NewRGBA(b)
		draw.Draw(rgba, b, img, b.Min, draw.Src)
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	c := core.NewCursor(id, rgba)
	m.images[id] = c
	m.mu.Unlock()
	return c
}

func (m *cursorImageDefault) CursorImageRemove(c core.Cursor) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.images[c.ID()]; !ok {
		return false
	}
	delete(m.images, c.ID())
	return true
}

func (m *cursorImageDefault) CursorImageFromFile(path string) (core.Cursor, error) {
	f, err := os.Open(path)
	if err != nil {
		return core.Cursor{}, fmt.Errorf("cursor: open %s: %w", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return core.Cursor{}, fmt.Errorf("cursor: decode %s: %w", path, err)
	}
	return m.CursorImageAdd(img), nil
}

func (m *cursorImageDefault) CursorImageGet(c core.Cursor) *image.RGBA {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.images[c.ID()]
	if !ok {
		return nil
	}
	return stored.Image()
}
