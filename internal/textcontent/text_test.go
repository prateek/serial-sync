package textcontent_test

import (
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/textcontent"
	"testing"
)

func TestVisibleBodyMeasurement(t *testing.T) {
	for _, tc := range []struct {
		name, html, plain, visible string
		count                      int
	}{
		{"entities", "<p>A &amp; B&nbsp; é</p>", "", "A & B é", 7},
		{"empty", "<p>  &nbsp; </p>", "incorrect fallback", "", 0},
		{"unicode", "<p>猫😀é</p>", "", "猫😀é", 3},
		{"hidden", "<style>ignore</style><p>Hello<br>world</p><script>ignore</script>", "", "Hello world", 11},
		{"html authoritative", "<p>Yes</p>", "a much longer plain body", "Yes", 3},
		{"plain", "", "  A\n\t &amp;  B ", "A & B", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := domain.NormalizedRelease{TextHTML: tc.html, TextPlain: tc.plain}
			if got := textcontent.Visible(release); got != tc.visible {
				t.Fatalf("visible=%q want %q", got, tc.visible)
			}
			if got := textcontent.Count(release); got != tc.count {
				t.Fatalf("count=%d want %d", got, tc.count)
			}
		})
	}
}
