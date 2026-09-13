package bookmark_test

import (
	"context"
	"errors"
	"github.com/keyno63/Shiorin/internal/bookmark"
	"sync"
	"testing"
)

func TestMemoryConcurrentAccessAndOwnership(t *testing.T) {
	m := &bookmark.Memory{}
	ctx := context.Background()
	in := bookmark.Input{Title: "sample", Tags: []string{"go"}}
	saved, err := m.Create(ctx, "alice", in)
	if err != nil {
		t.Fatal(err)
	}
	in.Tags[0] = "changed"
	saved.Tags[0] = "changed"
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Create(ctx, "alice", bookmark.Input{Title: "concurrent"}); err != nil {
				t.Error(err)
			}
			if _, _, err := m.Search(ctx, "alice", bookmark.Query{Limit: 100}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	items, total, err := m.Search(ctx, "alice", bookmark.Query{Tag: "go"})
	if err != nil || total != 1 {
		t.Fatalf("ownership: total=%d err=%v", total, err)
	}
	items[0].Tags[0] = "changed"
	_, total, err = m.Search(ctx, "alice", bookmark.Query{Tag: "go"})
	if err != nil || total != 1 {
		t.Fatal("search returned mutable stored data")
	}
	_, total, err = m.Search(ctx, "alice", bookmark.Query{})
	if err != nil || total != 21 {
		t.Fatalf("concurrent writes: total=%d err=%v", total, err)
	}
}

func TestRepositoryRequiresAndScopesOwner(t *testing.T) {
	m := &bookmark.Memory{}
	ctx := context.Background()
	if _, err := m.Create(ctx, "", bookmark.Input{}); !errors.Is(err, bookmark.ErrMissingUser) {
		t.Fatal("missing owner accepted")
	}
	if _, _, err := m.Search(ctx, "", bookmark.Query{}); !errors.Is(err, bookmark.ErrMissingUser) {
		t.Fatal("unscoped search accepted")
	}
	if _, err := m.Create(ctx, "alice", bookmark.Input{Title: "private"}); err != nil {
		t.Fatal(err)
	}
	items, total, err := m.Search(ctx, "bob", bookmark.Query{})
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatal("repository leaked another owner's data")
	}
}
