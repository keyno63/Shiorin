package bookmark

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

type Input struct {
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Note  string   `json:"note"`
	Tags  []string `json:"tags"`
}

type Bookmark struct {
	Input
	UserID    string    `json:"user_id"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type Query struct {
	Text, Tag     string
	Limit, Offset int
}

type Repository interface {
	Create(context.Context, string, Input) (Bookmark, error)
	Search(context.Context, string, Query) ([]Bookmark, int, error)
}

var ErrMissingUser = errors.New("user ID is required")

// Memory keeps bookmarks until the process exits.
type Memory struct {
	mu    sync.RWMutex
	items []Bookmark
}

var _ Repository = (*Memory)(nil)

func clone(b Bookmark) Bookmark {
	b.Tags = append([]string{}, b.Tags...)
	return b
}

func (m *Memory) Create(ctx context.Context, userID string, in Input) (Bookmark, error) {
	if strings.TrimSpace(userID) == "" {
		return Bookmark{}, ErrMissingUser
	}
	if err := ctx.Err(); err != nil {
		return Bookmark{}, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Bookmark{}, err
	}
	b := Bookmark{Input: in, UserID: userID, ID: hex.EncodeToString(id[:]), CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.items = append(m.items, clone(b))
	m.mu.Unlock()
	return clone(b), nil
}

func (m *Memory) Search(ctx context.Context, userID string, q Query) ([]Bookmark, int, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, 0, ErrMissingUser
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	text, tag := strings.ToLower(strings.TrimSpace(q.Text)), strings.ToLower(strings.TrimSpace(q.Tag))
	matches := []Bookmark{}
	for i := len(m.items) - 1; i >= 0; i-- {
		b := m.items[i]
		if b.UserID != userID {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(b.Title+" "+b.URL+" "+b.Note), text) {
			continue
		}
		hasTag := tag == ""
		for _, t := range b.Tags {
			if strings.ToLower(t) == tag {
				hasTag = true
				break
			}
		}
		if hasTag {
			matches = append(matches, b)
		}
	}
	total := len(matches)
	offset, limit := q.Offset, q.Limit
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset >= total {
		return []Bookmark{}, total, nil
	}
	end := total
	if limit < total-offset {
		end = offset + limit
	}
	result := make([]Bookmark, 0, end-offset)
	for _, b := range matches[offset:end] {
		result = append(result, clone(b))
	}
	return result, total, nil
}
