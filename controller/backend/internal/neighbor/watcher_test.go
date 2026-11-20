package neighbor

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"controller/backend/internal/store"
)

func openStore(t *testing.T) (*store.NeighborStore, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewNeighborStore(db), db
}

// One failed fetch must not empty the table; a source that stays down loses
// its hosts only after staleAfter.
func TestFetchKeepsLastGoodDataUntilStale(t *testing.T) {
	var failing atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failing.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`[{"ip":"fe80::1","lladdr":"02:00:00:00:00:01"},{"ip":"2001:db8::1"},{"ip":"FE80::0002"}]`))
	}))
	defer srv.Close()

	ns, _ := openStore(t)
	w := NewWatcher([]Source{{Name: "test", URL: srv.URL}}, 80*time.Millisecond, ns)
	count := func() int {
		t.Helper()
		if err := w.Fetch(context.Background()); err != nil {
			t.Fatal(err)
		}
		list, err := ns.List()
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}

	if got := count(); got != 2 { // the global address is not a host identity
		t.Fatalf("hosts after a good fetch = %d; want 2", got)
	}
	failing.Store(true)
	if got := count(); got != 2 {
		t.Fatalf("hosts after one failed fetch = %d; want 2 (last good data)", got)
	}
	if st := w.Statuses()[0]; st.OK || st.Error == "" || st.Count != 2 || st.LastSuccess == "" {
		t.Errorf("status of the failing source = %+v", st)
	}
	time.Sleep(120 * time.Millisecond)
	if got := count(); got != 0 {
		t.Fatalf("hosts after the data went stale = %d; want 0", got)
	}
	failing.Store(false)
	if got := count(); got != 2 {
		t.Fatalf("hosts after recovery = %d; want 2", got)
	}
	if st := w.Statuses()[0]; !st.OK || st.Error != "" {
		t.Errorf("status after recovery = %+v", st)
	}
}
