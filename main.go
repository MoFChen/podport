package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS
var version = "3.0.0"

func defaultDB() string {
	executable, err := os.Executable()
	if err != nil {
		executable, err = exec.LookPath(os.Args[0])
	}
	if err == nil {
		if abs, err := filepath.Abs(executable); err == nil {
			return filepath.Join(filepath.Dir(abs), "podport.db")
		}
	}
	return "podport.db"
}
func main() {
	flags := flag.NewFlagSet("podport", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:8090", "监听地址；容器中使用 0.0.0.0:8080")
	path := flags.String("db", defaultDB(), "SQLite 文件，默认放在程序旁边")
	showVersion := flags.Bool("version", false, "显示版本")
	checkHealth := flags.Bool("healthcheck", false, "检查容器内 127.0.0.1:8080")
	flags.Parse(os.Args[1:])
	if *showVersion {
		fmt.Println("PodPort", version)
		return
	}
	if *checkHealth {
		client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
		r, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	// This is a soft Go runtime target; the container's --memory flag is the hard cap.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(48 << 20)
	}
	store, err := openStore(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer store.db.Close()
	server := &http.Server{Addr: *listen, Handler: newHandler(store), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-stop.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	fmt.Printf("PodPort %s\n网页：http://%s\n数据库：%s\n", version, *listen, store.path)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, err error) {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		jsonResponse(w, 409, map[string]any{"error": err.Error(), "conflicts": conflict.Conflicts})
		return
	}
	if errors.Is(err, errNotFound) {
		jsonResponse(w, 404, map[string]string{"error": err.Error()})
		return
	}
	jsonResponse(w, 422, map[string]string{"error": err.Error()})
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return fmt.Errorf("JSON 输入无效或超出 5 MiB：%w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}
func newHandler(s *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.PingContext(r.Context()); err != nil {
			jsonResponse(w, 503, map[string]string{"error": "SQLite 不可用"})
			return
		}
		jsonResponse(w, 200, map[string]string{"status": "ok", "version": version})
	})
	mux.HandleFunc("GET /api/entries", func(w http.ResponseWriter, r *http.Request) {
		all, err := s.list()
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"entries": all, "schema_version": schemaVersion, "db": filepath.Base(s.path)})
	})
	mux.HandleFunc("POST /api/entries", func(w http.ResponseWriter, r *http.Request) {
		var e Entry
		if err := readJSON(w, r, &e); err != nil {
			apiError(w, err)
			return
		}
		saved, err := s.save(e, false)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 201, saved)
	})
	mux.HandleFunc("PUT /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		var e Entry
		if err := readJSON(w, r, &e); err != nil {
			apiError(w, err)
			return
		}
		e.ID = r.PathValue("id")
		saved, err := s.save(e, true)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, saved)
	})
	mux.HandleFunc("DELETE /api/entries/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.remove(r.PathValue("id")); err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/check", func(w http.ResponseWriter, r *http.Request) {
		var e Entry
		if err := readJSON(w, r, &e); err != nil {
			apiError(w, err)
			return
		}
		e, err := normalized(e)
		if err != nil {
			apiError(w, err)
			return
		}
		all, err := s.list()
		if err != nil {
			apiError(w, err)
			return
		}
		if c := entryConflicts(e, all); len(c) > 0 {
			apiError(w, &ConflictError{c})
			return
		}
		if _, err := mergedEntries(e, all); err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {
		all, err := s.list()
		if err != nil {
			apiError(w, err)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="podport.json"`)
		jsonResponse(w, 200, Snapshot{schemaVersion, all})
	})
	mux.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		path, err := s.backup()
		if err != nil {
			apiError(w, err)
			return
		}
		defer os.Remove(path)
		f, err := os.Open(path)
		if err != nil {
			apiError(w, err)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/vnd.sqlite3")
		w.Header().Set("Content-Disposition", `attachment; filename="podport-backup.db"`)
		http.ServeContent(w, r, "podport-backup.db", time.Now(), f)
	})
	readImport := func(w http.ResponseWriter, r *http.Request) ([]Entry, error) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
		if err != nil {
			return nil, err
		}
		return decodeSnapshot(raw)
	}
	mux.HandleFunc("POST /api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		all, err := readImport(w, r)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true, "count": len(all)})
	})
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PodPort-Confirm") != "replace" {
			apiError(w, fmt.Errorf("请先预览并确认替换当前登记"))
			return
		}
		all, err := readImport(w, r)
		if err != nil {
			apiError(w, err)
			return
		}
		if err = s.replace(all); err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 404, map[string]string{"error": "接口不存在"})
	})
	sub, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					jsonResponse(w, 403, map[string]string{"error": "不允许跨站写入"})
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}
