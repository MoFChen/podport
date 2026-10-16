package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type flatEntry struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Service   string `json:"service"`
	HostIP    string `json:"host_ip"`
	HostStart int    `json:"host_start"`
	HostEnd   int    `json:"host_end"`
	ListenIP  string `json:"listen_ip"`
	PortStart int    `json:"port_start"`
	PortEnd   int    `json:"port_end"`
	Protocol  string `json:"protocol"`
	Scope     string `json:"scope"`
	Status    string `json:"status"`
	Note      string `json:"note"`
	UpdatedAt string `json:"updated_at"`
}
type legacyHost struct{ ID, Name string }
type legacyPod struct {
	ID          string
	HostID      string `json:"host_id"`
	Name        string
	NetworkMode string          `json:"network_mode"`
	ShareNet    json.RawMessage `json:"share_net"`
}
type legacyContainer struct {
	ID          string
	HostID      string  `json:"host_id"`
	PodID       *string `json:"pod_id"`
	Name        string
	NetworkMode string `json:"network_mode"`
}
type legacyListener struct {
	ID                              string
	ContainerID                     string `json:"container_id"`
	ListenIP                        string `json:"listen_ip"`
	PortStart                       int    `json:"port_start"`
	PortEnd                         int    `json:"port_end"`
	Protocol, Status, Service, Note string
	UpdatedAt                       string `json:"updated_at"`
}
type legacyPublication struct {
	ID         string
	ListenerID string `json:"listener_id"`
	HostIP     string `json:"host_ip"`
	PortStart  int    `json:"port_start"`
	PortEnd    int    `json:"port_end"`
	Status     string
}
type legacySnapshot struct {
	SchemaVersion int                 `json:"schema_version"`
	Hosts         []legacyHost        `json:"hosts"`
	Pods          []legacyPod         `json:"pods"`
	Containers    []legacyContainer   `json:"containers"`
	Listeners     []legacyListener    `json:"listeners"`
	Publications  []legacyPublication `json:"publications"`
}

func legacyEntries(old legacySnapshot) ([]flatEntry, error) {
	hosts := map[string]legacyHost{}
	pods := map[string]legacyPod{}
	containers := map[string]legacyContainer{}
	for _, h := range old.Hosts {
		hosts[h.ID] = h
	}
	for _, p := range old.Pods {
		pods[p.ID] = p
	}
	for _, c := range old.Containers {
		containers[c.ID] = c
	}
	entries := []flatEntry{}
	for _, l := range old.Listeners {
		c, ok := containers[l.ContainerID]
		if !ok {
			return nil, fmt.Errorf("旧数据缺少容器")
		}
		h, ok := hosts[c.HostID]
		if !ok {
			return nil, fmt.Errorf("旧数据缺少主机")
		}
		e := flatEntry{ID: l.ID, Host: h.Name, Container: c.Name, Service: l.Service, ListenIP: l.ListenIP, PortStart: l.PortStart, PortEnd: l.PortEnd, Protocol: l.Protocol, Status: l.Status, Note: l.Note, UpdatedAt: l.UpdatedAt, Scope: "container"}
		if c.PodID != nil {
			p, ok := pods[*c.PodID]
			if !ok {
				return nil, fmt.Errorf("旧数据缺少 Pod")
			}
			e.Pod = p.Name
			if string(p.ShareNet) == "true" || string(p.ShareNet) == "1" {
				e.Scope = "pod"
			}
			if p.NetworkMode == "host" {
				e.Scope = "host"
			}
		}
		if c.NetworkMode == "host" {
			e.Scope = "host"
		}
		mapped := false
		for _, m := range old.Publications {
			if m.ListenerID != l.ID {
				continue
			}
			out := e
			out.ID = m.ID
			out.HostIP = m.HostIP
			out.HostStart = m.PortStart
			out.HostEnd = m.PortEnd
			if l.Status != "released" && m.Status == "reserved" {
				out.Status = "reserved"
			}
			if m.Status == "released" {
				out.Status = "released"
			} else {
				mapped = true
			}
			entries = append(entries, out)
		}
		// A service with all mappings released may still listen internally.
		if !mapped {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func convertFlat(rows []flatEntry) ([]Entry, error) {
	out := []Entry{}
	groups := map[string]int{}
	keys := map[string]map[string]int{}
	for _, r := range rows {
		r.Host = strings.TrimSpace(r.Host)
		if r.Host == "" {
			r.Host = "本机"
		}
		r.Container = strings.TrimSpace(r.Container)
		r.Pod = strings.TrimSpace(r.Pod)
		if r.Scope == "" || r.Scope == "auto" {
			r.Scope = "container"
			if r.Pod != "" {
				r.Scope = "pod"
			}
		}
		if r.ListenIP == "" {
			r.ListenIP = "0.0.0.0"
		}
		if r.HostIP == "" {
			r.HostIP = "0.0.0.0"
		}
		if r.Protocol == "" {
			r.Protocol = "tcp"
		}
		if r.Status == "" {
			r.Status = "active"
		}

		k := strings.ToLower(strings.TrimSpace(r.Host)) + "\x00" + strings.TrimSpace(r.Container)
		i, ok := groups[k]
		if !ok {
			i = len(out)
			groups[k] = i
			out = append(out, Entry{ID: r.ID, Host: r.Host, Pod: r.Pod, Container: r.Container, Scope: r.Scope, Networks: []string{}, Listeners: []Listener{}, UpdatedAt: r.UpdatedAt})
			keys[k] = map[string]int{}
		}
		e := &out[i]
		if e.Pod != r.Pod || e.Scope != r.Scope {
			return nil, fmt.Errorf("旧数据的同名容器具有不同 Pod 或网络模式，请先在旧版修正")
		}
		if r.PortEnd == 0 {
			r.PortEnd = r.PortStart
		}
		lk := fmt.Sprintf("%s|%d|%d|%s", r.ListenIP, r.PortStart, r.PortEnd, r.Protocol)
		j, ok := keys[k][lk]
		if !ok {
			j = len(e.Listeners)
			keys[k][lk] = j
			e.Listeners = append(e.Listeners, Listener{ID: r.ID, Service: r.Service, ListenIP: r.ListenIP, PortStart: r.PortStart, PortEnd: r.PortEnd, Protocol: r.Protocol, Status: r.Status, Note: r.Note, Publications: []Publication{}})
		}
		l := &e.Listeners[j]
		if r.Status == "active" || r.Status == "" {
			l.Status = "active"
		} else if r.Status == "reserved" && l.Status == "released" {
			l.Status = "reserved"
		}
		if r.Note != "" && !strings.Contains(l.Note, r.Note) {
			l.Note += "\n" + r.Note
		}
		if r.Service != "" && !strings.Contains(l.Service, r.Service) {
			l.Service += " / " + r.Service
		}
		if r.HostStart > 0 {
			l.Publications = append(l.Publications, Publication{HostIP: r.HostIP, HostStart: r.HostStart, HostEnd: r.HostEnd, Protocol: r.Protocol, Status: r.Status})
		}
	}
	return validateEntries(out)
}
func decodeSnapshot(raw []byte) ([]Entry, error) {
	var h struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("不是有效的 JSON 文件")
	}
	if h.SchemaVersion == 1 {
		var old legacySnapshot
		if err := json.Unmarshal(raw, &old); err != nil {
			return nil, err
		}
		rows, err := legacyEntries(old)
		if err != nil {
			return nil, err
		}
		return convertFlat(rows)
	}
	if h.SchemaVersion == 2 {
		var old struct {
			Entries []flatEntry `json:"entries"`
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return nil, err
		}
		if old.Entries == nil {
			return nil, fmt.Errorf("JSON 缺少 entries 数组")
		}
		return convertFlat(old.Entries)
	}
	if h.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("仅支持 PodPort v1/v2/v3 导出的 JSON 文件")
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	if snap.Entries == nil {
		return nil, fmt.Errorf("JSON 缺少 entries 数组")
	}
	return validateEntries(snap.Entries)
}
