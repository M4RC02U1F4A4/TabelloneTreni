package trenord

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestFetchDalVivo interroga davvero Trenord. Sta fuori dai test normali
// perché dipende dalla rete, ma è l'unico che accorge di due cose che una
// fixture congelata non può vedere: che l'endpoint sia ancora lì, e che
// l'anti-bot continui a farci passare con lo User-Agent che usiamo.
//
//	TRENORD_LIVE=1 go test ./internal/trenord/ -run DalVivo -v
func TestFetchDalVivo(t *testing.T) {
	if os.Getenv("TRENORD_LIVE") == "" {
		t.Skip("richiede rete: TRENORD_LIVE=1 per eseguirlo")
	}
	ctx, annulla := context.WithTimeout(context.Background(), 30*time.Second)
	defer annulla()

	linee, err := NewClient().Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d linee", len(linee))
	for _, l := range linee {
		if l.Stato != Regolare {
			t.Logf("%s %s (%s): %s", l.Codice, l.Nome, l.Gruppo, l.Stato)
		}
	}
	if len(linee) < 40 {
		t.Errorf("solo %d linee: la pagina ne elenca una sessantina", len(linee))
	}
}
