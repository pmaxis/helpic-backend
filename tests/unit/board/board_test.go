package board_test

import (
	"slices"
	"testing"

	"github.com/pmaxis/helpic/internal/board"
	"github.com/pmaxis/helpic/internal/tasks"
)

func TestArrange(t *testing.T) {
	pos := func(n int) *int { return &n }
	cols := []board.Column{{ID: "todo"}, {ID: "review"}, {ID: "done"}}
	list := []tasks.Task{
		{ID: "a", StatusID: "todo"},
		{ID: "b", StatusID: "todo", Position: pos(0)}, // ordered cards come first
		{ID: "c", StatusID: "review"},
		{ID: "d", StatusID: "todo", Position: pos(1)},
		{ID: "x", StatusID: "elsewhere"}, // not on this board
	}

	want := map[string][]string{"todo": {"b", "d", "a"}, "review": {"c"}, "done": {}}
	for _, l := range board.Arrange(cols, list) {
		got := []string{}
		for _, t := range l.Tasks {
			got = append(got, t.ID)
		}
		if !slices.Equal(got, want[l.Column.ID]) {
			t.Errorf("%s = %v, want %v", l.Column.ID, got, want[l.Column.ID])
		}
	}
}
