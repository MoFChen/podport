package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "podport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s
}
func listener(port int, protocol string, outer int) Listener {
	l := Listener{ID: newID(), PortStart: port, Protocol: protocol, Service: "service"}
	if outer > 0 {
		l.Publications = []Publication{{HostStart: outer, Protocol: protocol}}
	}
	return l
}
func sample() Entry {
	return Entry{Host: "nas", Pod: "web", Container: "nginx", Scope: "pod", Networks: []string{"media-net"}, Listeners: []Listener{listener(80, "tcp", 8080)}}
}
func saveOK(t *testing.T, s *Store, e Entry) Entry {
	t.Helper()
	v, err := s.save(e, false)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestConflictRules(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Entry)
		want   bool
	}{
		{"host wildcard", func(e *Entry) {
			e.Pod = "other"
			e.Container = "two"
			e.Listeners[0].Publications[0].HostIP = "127.0.0.1"
		}, true},
		{"same Pod different published port", func(e *Entry) { e.Container = "two"; e.Listeners[0].Publications[0].HostStart = 8181 }, true},
		{"different host", func(e *Entry) { e.Host = "mini" }, false},
		{"UDP separate from TCP", func(e *Entry) {
			e.Container = "two"
			e.Listeners[0].Protocol = "udp"
			e.Listeners[0].Publications[0].Protocol = "udp"
		}, false},
		{"both overlaps TCP", func(e *Entry) {
			e.Container = "two"
			e.Listeners[0].Protocol = "both"
			e.Listeners[0].Publications[0].Protocol = "both"
		}, true},
		{"independent container same bridge same internal port", func(e *Entry) {
			e.Pod = ""
			e.Scope = "container"
			e.Container = "emby"
			e.Listeners[0].Publications = nil
		}, false},
		{"different Pod same bridge same internal port", func(e *Entry) { e.Pod = "other"; e.Container = "two"; e.Listeners[0].Publications[0].HostStart = 8181 }, false},
		{"reserved occupies", func(e *Entry) { e.Container = "two"; e.Listeners[0].Status = "reserved" }, true},
		{"released frees all", func(e *Entry) { e.Container = "two"; e.Listeners[0].Status = "released" }, false},
		{"specific internal binding", func(e *Entry) {
			e.Container = "two"
			e.Listeners[0].ListenIP = "127.0.0.1"
			e.Listeners[0].Publications = nil
		}, true},
		{"host network collides with publication", func(e *Entry) {
			e.Pod = ""
			e.Scope = "host"
			e.Networks = nil
			e.Container = "two"
			e.Listeners = []Listener{listener(8080, "tcp", 0)}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t)
			saveOK(t, s, sample())
			e := sample()
			tt.change(&e)
			_, err := s.save(e, false)
			_, got := err.(*ConflictError)
			if got != tt.want {
				t.Fatalf("want conflict=%v, got %v", tt.want, err)
			}
			if err != nil && !got {
				t.Fatal(err)
			}
		})
	}
}
func TestMultiListenersPublicationsAndDualProtocol(t *testing.T) {
	s := testStore(t)
	e := sample()
	e.Listeners = append(e.Listeners, listener(443, "both", 8443), listener(9090, "tcp", 0))
	e.Listeners[0].Publications = append(e.Listeners[0].Publications, Publication{HostStart: 8181, Protocol: "tcp"})
	a := saveOK(t, s, e)
	all, _ := s.list()
	if len(all) != 1 || len(all[0].Listeners) != 3 || len(all[0].Listeners[0].Publications) != 2 {
		t.Fatal(all)
	}
	b := Entry{Host: "nas", Container: "sync", Scope: "container", Listeners: []Listener{listener(22000, "both", 22000), listener(21027, "udp", 21027), listener(8384, "tcp", 8384)}}
	saveOK(t, s, b)
	e = Entry{Host: "nas", Container: "other", Listeners: []Listener{listener(1234, "udp", 22000)}}
	if _, err := s.save(e, false); err == nil {
		t.Fatal("both must reserve UDP publication")
	}
	a.Listeners[1].Publications[0].Protocol = "tcp"
	if _, err := s.save(a, true); err != nil {
		t.Fatal(err)
	}
	e.Listeners[0].Publications[0].HostStart = 8443
	if _, err := s.save(e, false); err != nil {
		t.Fatal("UDP publication should be free after mapping changed to TCP", err)
	}
}
func TestSelfConflictsAndPortRelease(t *testing.T) {
	for _, kind := range []string{"internal", "publication", "both", "range"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			e := sample()
			switch kind {
			case "internal":
				e.Listeners = append(e.Listeners, listener(80, "tcp", 0))
			case "publication":
				e.Listeners[0].Publications = append(e.Listeners[0].Publications, Publication{HostStart: 8080})
			case "both":
				e.Listeners[0].Protocol = "both"
				e.Listeners = append(e.Listeners, listener(80, "udp", 0))
			case "range":
				e.Listeners[0] = listener(8000, "tcp", 0)
				e.Listeners[0].PortEnd = 8010
				e.Listeners = append(e.Listeners, listener(8009, "tcp", 0))
			}
			if _, err := s.save(e, false); err == nil {
				t.Fatal("self conflict accepted")
			}
		})
	}
	s := testStore(t)
	a := sample()
	a.Listeners[0].Publications[0].Status = "released"
	a = saveOK(t, s, a)
	b := sample()
	b.Pod = "other"
	b.Container = "two"
	saveOK(t, s, b)
	c := sample()
	c.Container = "three"
	c.Listeners[0].Publications = nil
	if _, err := s.save(c, false); err == nil {
		t.Fatal("released publication must retain internal listener")
	}
	a.Listeners[0].Status = "released"
	if _, err := s.save(a, true); err != nil {
		t.Fatal(err)
	}
	saveOK(t, s, c)
}
func TestPodNetworkMembershipAtomicAndMigrationIndependentNamespace(t *testing.T) {
	s := testStore(t)
	a := saveOK(t, s, sample())
	b := sample()
	b.Container = "api"
	b.Listeners = []Listener{listener(3000, "tcp", 0)}
	b = saveOK(t, s, b)
	emby := Entry{Host: "nas", Container: "emby", Networks: []string{"media-net"}, Listeners: []Listener{listener(8096, "tcp", 0), listener(8920, "tcp", 0)}}
	saveOK(t, s, emby)
	a.Networks = []string{"frontend", "media-net"}
	a.Listeners = append(a.Listeners, listener(3000, "tcp", 0))
	if _, err := s.save(a, true); err == nil {
		t.Fatal("bad edit accepted")
	}
	all, _ := s.list()
	for _, e := range all {
		if e.ID == b.ID && !slices.Equal(e.Networks, []string{"media-net"}) {
			t.Fatal("failed edit leaked networks")
		}
	}
	a.Listeners = a.Listeners[:1]
	if _, err := s.save(a, true); err != nil {
		t.Fatal(err)
	}
	all, _ = s.list()
	for _, e := range all {
		if e.Scope == "pod" && !slices.Equal(e.Networks, []string{"frontend", "media-net"}) {
			t.Fatal("Pod networks not propagated")
		}
		if e.Container == "emby" && !slices.Equal(e.Networks, []string{"media-net"}) {
			t.Fatal("independent network changed")
		}
	}
}
func TestSpecificAddressesAndRanges(t *testing.T) {
	s := testStore(t)
	a := sample()
	a.Listeners[0].ListenIP = "127.0.0.1"
	a.Listeners[0].Publications[0].HostIP = "127.0.0.1"
	saveOK(t, s, a)
	b := sample()
	b.Container = "two"
	b.Listeners[0].ListenIP = "10.88.0.2"
	b.Listeners[0].Publications[0].HostIP = "10.0.0.2"
	saveOK(t, s, b)
	c := Entry{Host: "nas", Container: "ranged", Listeners: []Listener{listener(8000, "both", 9000)}}
	c.Listeners[0].PortEnd = 8010
	c.Listeners[0].Publications[0].HostEnd = 9010
	saveOK(t, s, c)
	d := Entry{Host: "nas", Container: "ranged2", Listeners: []Listener{listener(90, "udp", 9010)}}
	if _, err := s.save(d, false); err == nil {
		t.Fatal("range overlap accepted")
	}
	d.Listeners[0].Publications[0].HostStart = 9011
	saveOK(t, s, d)
}
func TestInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Entry)
	}{
		{"zero", func(e *Entry) { e.Listeners[0].PortStart = 0 }}, {"max", func(e *Entry) { e.Listeners[0].PortStart = 65536 }}, {"reversed", func(e *Entry) { e.Listeners[0].PortEnd = 79 }}, {"range lengths", func(e *Entry) { e.Listeners[0].PortEnd = 81 }}, {"IPv6", func(e *Entry) { e.Listeners[0].ListenIP = "::" }}, {"invalid IP", func(e *Entry) { e.Listeners[0].ListenIP = "999.0.0.1" }}, {"container", func(e *Entry) { e.Container = "" }}, {"protocol", func(e *Entry) { e.Listeners[0].Protocol = "sctp" }}, {"mapping protocol subset", func(e *Entry) { e.Listeners[0].Publications[0].Protocol = "both" }}, {"host publication", func(e *Entry) { e.Scope = "host"; e.Networks = nil }}, {"host networks", func(e *Entry) { e.Scope = "host"; e.Listeners[0].Publications = nil }}, {"missing Pod", func(e *Entry) { e.Pod = "" }}, {"network name", func(e *Entry) { e.Networks = []string{"bad\nname"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := sample()
			tt.change(&e)
			if _, err := normalized(e); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	s := testStore(t)
	a := sample()
	saveOK(t, s, a)
	a.Pod = "other"
	a.Listeners = nil
	if _, err := s.save(a, false); err == nil {
		t.Fatal("duplicate container name accepted")
	}
}
func TestConcurrentReservation(t *testing.T) {
	s := testStore(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e := sample()
			e.Pod = fmt.Sprint(i)
			e.Container = fmt.Sprint(i)
			_, err := s.save(e, false)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, bad := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if _, is := err.(*ConflictError); is {
			bad++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || bad != 1 {
		t.Fatal(ok, bad)
	}
}
func TestFilesRestartBackupAndEmptyContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podport.db")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	saveOK(t, s, sample())
	saveOK(t, s, Entry{Container: "empty", Networks: []string{"media-net"}})
	backup, err := s.backup()
	if err != nil {
		t.Fatal(err)
	}
	other, err := openStore(backup)
	if err != nil {
		t.Fatal(err)
	}
	var integrity string
	other.db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	other.db.Close()
	os.Remove(backup)
	s.db.Close()
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatal("persistent sidecars", files)
	}
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	all, _ := s.list()
	if len(all) != 2 {
		t.Fatal(all)
	}
	var mode string
	s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "delete" {
		t.Fatal(mode)
	}
}
func TestImportRollback(t *testing.T) {
	s := testStore(t)
	saveOK(t, s, sample())
	a := sample()
	a.ID = "a"
	b := sample()
	b.ID = "b"
	b.Container = "two"
	if err := s.replace([]Entry{a, b}); err == nil {
		t.Fatal("bad import accepted")
	}
	all, _ := s.list()
	if len(all) != 1 {
		t.Fatal(all)
	}
	b.Listeners = []Listener{listener(3000, "tcp", 0)}
	b.Networks = nil
	if err := s.replace([]Entry{a, b}); err == nil {
		t.Fatal("inconsistent Pod memberships accepted")
	}
	b.Networks = a.Networks
	if err := s.replace([]Entry{a, b}); err != nil {
		t.Fatal(err)
	}
}
func request(t *testing.T, s *Store, method, path string, body any, expected int) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newHandler(s).ServeHTTP(w, r)
	if w.Code != expected {
		t.Fatalf("%s %s wanted %d got %d %s", method, path, expected, w.Code, w.Body.String())
	}
	return w
}
func TestAPIAndEmbeddedUI(t *testing.T) {
	s := testStore(t)
	request(t, s, "GET", "/healthz", nil, 200)
	for _, path := range []string{"/", "/app.js", "/model.js", "/graph.js", "/theme.js", "/style.css"} {
		w := request(t, s, "GET", path, nil, 200)
		if w.Body.Len() == 0 {
			t.Fatal(path)
		}
	}
	request(t, s, "POST", "/api/entries", sample(), 201)
	request(t, s, "POST", "/api/check", sample(), 409)
	w := request(t, s, "GET", "/api/export", nil, 200)
	entries, err := decodeSnapshot(w.Body.Bytes())
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	request(t, s, "POST", "/api/import/preview", Snapshot{3, entries}, 200)
	request(t, s, "POST", "/api/import", Snapshot{3, entries}, 422)
	r := httptest.NewRequest("POST", "/api/import", bytes.NewReader(w.Body.Bytes()))
	r.Header.Set("X-PodPort-Confirm", "replace")
	rr := httptest.NewRecorder()
	newHandler(s).ServeHTTP(rr, r)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	all, _ := s.list()
	request(t, s, "GET", "/api/backup", nil, 200)
	files, _ := os.ReadDir(filepath.Dir(s.path))
	if len(files) != 1 {
		t.Fatal("snapshot leaked", files)
	}
	request(t, s, "DELETE", "/api/entries/"+all[0].ID, nil, 200)
	request(t, s, "DELETE", "/api/entries/"+all[0].ID, nil, 404)
	r = httptest.NewRequest("POST", "/api/entries", nil)
	r.Header.Set("Origin", "https://other.example")
	rr = httptest.NewRecorder()
	newHandler(s).ServeHTTP(rr, r)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
}

const v1Fixture = `{"schema_version":1,"hosts":[{"id":"h","name":"nas"}],"pods":[{"id":"p","host_id":"h","name":"web","network_mode":"isolated","share_net":1}],"containers":[{"id":"c","host_id":"h","pod_id":"p","name":"nginx","network_mode":"inherit"}],"listeners":[{"id":"l","container_id":"c","listen_ip":"0.0.0.0","port_start":80,"port_end":80,"protocol":"tcp","status":"active","service":"web"}],"publications":[{"id":"m1","listener_id":"l","host_ip":"0.0.0.0","port_start":8080,"port_end":8080,"status":"active"},{"id":"m2","listener_id":"l","host_ip":"0.0.0.0","port_start":8181,"port_end":8181,"status":"released"}]}`

func legacyDB(t *testing.T, path string, tables map[string][]map[string]any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, items := range tables {
		names := []string{}
		for k := range items[0] {
			names = append(names, k)
		}
		q := "CREATE TABLE " + table + "("
		for i, n := range names {
			if i > 0 {
				q += ","
			}
			q += n
		}
		q += ")"
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
		for _, row := range items {
			args := []any{}
			qs := ""
			for i, n := range names {
				if i > 0 {
					qs += ","
				}
				qs += "?"
				args = append(args, row[n])
			}
			if _, err = db.Exec("INSERT INTO "+table+" VALUES("+qs+")", args...); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestV1JSONAndDatabaseMigration(t *testing.T) {
	all, err := decodeSnapshot([]byte(v1Fixture))
	if err != nil || len(all) != 1 || len(all[0].Listeners) != 1 || len(all[0].Listeners[0].Publications) != 2 {
		t.Fatal(all, err)
	}
	var data map[string]json.RawMessage
	json.Unmarshal([]byte(v1Fixture), &data)
	tables := map[string][]map[string]any{}
	for _, name := range []string{"hosts", "pods", "containers", "listeners", "publications"} {
		var rows []map[string]any
		json.Unmarshal(data[name], &rows)
		tables[name] = rows
	}
	path := filepath.Join(t.TempDir(), "old.db")
	legacyDB(t, path, tables)
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	all, _ = s.list()
	if len(all) != 1 {
		t.Fatal(all)
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	all, _ = s.list()
	if len(all) != 1 {
		t.Fatal("migration repeated")
	}
	var count int
	s.db.QueryRowContext(context.Background(), "SELECT count(*) FROM listeners").Scan(&count)
	if count != 1 {
		t.Fatal("original table lost")
	}
}
func TestV2MigrationMergesListenersAndPreservesReleasedMapping(t *testing.T) {
	base := flatEntry{ID: "one", Host: "nas", Pod: "web", Container: "nginx", Scope: "pod", ListenIP: "0.0.0.0", PortStart: 80, PortEnd: 80, Protocol: "tcp", Status: "active", HostIP: "0.0.0.0", HostStart: 8080, HostEnd: 8080}
	second := base
	second.ID = "two"
	second.HostStart = 8181
	second.HostEnd = 8181
	second.Status = "released"
	third := base
	third.ID = "three"
	third.PortStart = 443
	third.PortEnd = 443
	third.HostStart = 0
	third.HostEnd = 0
	raw, _ := json.Marshal(map[string]any{"schema_version": 2, "entries": []flatEntry{base, second, third}})
	all, err := decodeSnapshot(raw)
	if err != nil || len(all) != 1 || len(all[0].Listeners) != 2 || len(all[0].Listeners[0].Publications) != 2 {
		t.Fatal(all, err)
	}
	l := all[0].Listeners[0]
	if l.Status != "active" || l.Publications[1].Status != "released" {
		t.Fatal(l)
	}
	var data struct {
		Entries []map[string]any `json:"entries"`
	}
	json.Unmarshal(raw, &data)
	path := filepath.Join(t.TempDir(), "v2.db")
	legacyDB(t, path, map[string][]map[string]any{"port_entries": data.Entries})
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	all, _ = s.list()
	if len(all) != 1 {
		t.Fatal(all)
	}
	if err = s.remove(all[0].ID); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	all, _ = s.list()
	if len(all) != 0 {
		t.Fatal("old records resurrected after deletion")
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM port_entries").Scan(&count)
	if count != 3 {
		t.Fatal("original v2 table changed")
	}
}
func TestV1ReleasedPublicationDoesNotReleaseListener(t *testing.T) {
	var old legacySnapshot
	json.Unmarshal([]byte(v1Fixture), &old)
	for i := range old.Publications {
		old.Publications[i].Status = "released"
	}
	raw, _ := json.Marshal(old)
	all, err := decodeSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Listeners[0].Status != "active" || len(all[0].Listeners[0].Publications) != 2 {
		t.Fatal(all)
	}
}
