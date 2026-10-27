package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestReadingPositionNoticePrefersDefiniteReplacements(t *testing.T) {
	conditional := domain.PublishItemResult{TargetID: "first", Action: "replace", Conditional: true}
	definite := domain.PublishItemResult{TargetID: "second", Action: "retire"}
	for _, tc := range []struct {
		name  string
		items []domain.PublishItemResult
		want  string
	}{
		{"no replacement", []domain.PublishItemResult{{Action: "unchanged"}}, ""},
		{"conditional only", []domain.PublishItemResult{conditional}, "Volume delivery is conditional"},
		{"definite follows conditional", []domain.PublishItemResult{conditional, definite}, "Replacing delivered books"},
		{"definite precedes conditional", []domain.PublishItemResult{definite, conditional}, "Replacing delivered books"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]domain.PublishItemResult(nil), tc.items...)
			notice := readingPositionNotice(tc.items)
			if tc.want == "" && notice != "" || tc.want != "" && !strings.HasPrefix(notice, tc.want) {
				t.Fatalf("notice: %q, want prefix %q", notice, tc.want)
			}
			if !reflect.DeepEqual(before, tc.items) {
				t.Fatal("notice selection changed item-level flags")
			}
		})
	}
}
