package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"
)

const maxEntries = 5000
const schemaVersion = 3

// A container owns listeners. Pod members share network membership and a namespace;
// containers on the same bridge still have separate network namespaces.
type Entry struct {
	ID        string     `json:"id"`
	Host      string     `json:"host"`
	Pod       string     `json:"pod"`
	Container string     `json:"container"`
	Scope     string     `json:"scope"`
	Networks  []string   `json:"networks"`
	Listeners []Listener `json:"listeners"`
	Note      string     `json:"note"`
	UpdatedAt string     `json:"updated_at"`
}
type Listener struct {
	ID           string        `json:"id"`
	Service      string        `json:"service"`
	ListenIP     string        `json:"listen_ip"`
	PortStart    int           `json:"port_start"`
	PortEnd      int           `json:"port_end"`
	Protocol     string        `json:"protocol"`
	Status       string        `json:"status"`
	Note         string        `json:"note"`
	Publications []Publication `json:"publications"`
}
type Publication struct {
	HostIP    string `json:"host_ip"`
	HostStart int    `json:"host_start"`
	HostEnd   int    `json:"host_end"`
	Protocol  string `json:"protocol"`
	Status    string `json:"status"`
}
type Snapshot struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}
type Conflict struct {
	Kind      string `json:"kind"`
	WithID    string `json:"with_id"`
	WithLabel string `json:"with_label"`
	PortStart int    `json:"port_start"`
	PortEnd   int    `json:"port_end"`
	Protocol  string `json:"protocol"`
}
type ConflictError struct{ Conflicts []Conflict }

func (e *ConflictError) Error() string {
	return "端口与已有登记冲突，请调整端口或绑定地址"
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func validName(v string) bool {
	return len([]rune(v)) <= 100 && strings.IndexFunc(v, unicode.IsControl) < 0
}
func validIP(v string) bool { a, err := netip.ParseAddr(v); return err == nil && a.Is4() }
func protocols(p string) []string {
	if p == "both" {
		return []string{"tcp", "udp"}
	}
	return []string{p}
}
func validProtocol(p string) bool { return p == "tcp" || p == "udp" || p == "both" }
func validStatus(p string) bool   { return p == "active" || p == "reserved" || p == "released" }
func normalized(e Entry) (Entry, error) {
	e.Host = strings.TrimSpace(e.Host)
	if e.Host == "" {
		e.Host = "本机"
	}
	e.Pod = strings.TrimSpace(e.Pod)
	e.Container = strings.TrimSpace(e.Container)
	e.Note = strings.TrimSpace(e.Note)
	if e.Container == "" {
		return e, fmt.Errorf("请填写容器名称")
	}
	for _, v := range []string{e.Host, e.Pod, e.Container} {
		if !validName(v) {
			return e, fmt.Errorf("名称不能包含控制字符，长度不能超过 100 字")
		}
	}
	if len([]rune(e.Note)) > 1000 {
		return e, fmt.Errorf("备注不能超过 1000 字")
	}
	if e.Scope == "" || e.Scope == "auto" {
		e.Scope = "container"
		if e.Pod != "" {
			e.Scope = "pod"
		}
	}
	if e.Scope != "pod" && e.Scope != "container" && e.Scope != "host" || e.Scope == "pod" && e.Pod == "" {
		return e, fmt.Errorf("网络模式与 Pod 不匹配")
	}
	nets := []string{}
	for _, n := range e.Networks {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !validName(n) || strings.ContainsAny(n, ",，;；") {
			return e, fmt.Errorf("网络名称无效")
		}
		if !slices.Contains(nets, n) {
			nets = append(nets, n)
		}
	}
	slices.Sort(nets)
	e.Networks = nets
	if len(nets) > 32 {
		return e, fmt.Errorf("每个网络命名空间最多登记 32 个网络")
	}
	if e.Scope == "host" && len(nets) > 0 {
		return e, fmt.Errorf("host 网络不能同时加入自定义网络")
	}
	if len(e.Listeners) > 128 {
		return e, fmt.Errorf("每个容器最多登记 128 个监听端口")
	}
	listeners := make([]Listener, 0, len(e.Listeners))
	ids := map[string]bool{}
	for _, l := range e.Listeners {
		if l.ID == "" {
			l.ID = newID()
		}
		if len(l.ID) > 120 || ids[l.ID] {
			return e, fmt.Errorf("监听 ID 无效或重复")
		}
		ids[l.ID] = true
		l.Service = strings.TrimSpace(l.Service)
		l.Note = strings.TrimSpace(l.Note)
		if !validName(l.Service) || len([]rune(l.Note)) > 1000 {
			return e, fmt.Errorf("端口名称或备注过长")
		}
		if l.ListenIP == "" {
			l.ListenIP = "0.0.0.0"
		}
		if !validIP(l.ListenIP) {
			return e, fmt.Errorf("请使用明确的 IPv4 监听地址")
		}
		if l.PortEnd == 0 {
			l.PortEnd = l.PortStart
		}
		if l.PortStart < 1 || l.PortEnd < l.PortStart || l.PortEnd > 65535 {
			return e, fmt.Errorf("内部端口应为 1–65535 内的单个端口或递增范围")
		}
		if l.Protocol == "" {
			l.Protocol = "tcp"
		}
		if !validProtocol(l.Protocol) {
			return e, fmt.Errorf("协议必须是 TCP、UDP 或 TCP + UDP")
		}
		if l.Status == "" {
			l.Status = "active"
		}
		if !validStatus(l.Status) {
			return e, fmt.Errorf("登记状态无效")
		}
		if len(l.Publications) > 64 {
			return e, fmt.Errorf("一个监听最多登记 64 个主机映射")
		}
		pubs := make([]Publication, 0, len(l.Publications))
		for _, p := range l.Publications {
			if e.Scope == "host" {
				return e, fmt.Errorf("host 网络直接使用主机监听端口，无须对外映射")
			}
			if p.HostIP == "" {
				p.HostIP = "0.0.0.0"
			}
			if !validIP(p.HostIP) {
				return e, fmt.Errorf("请使用明确的 IPv4 主机绑定地址")
			}
			if p.HostEnd == 0 {
				p.HostEnd = p.HostStart
			}
			if p.HostStart < 1 || p.HostEnd < p.HostStart || p.HostEnd > 65535 || p.HostEnd-p.HostStart != l.PortEnd-l.PortStart {
				return e, fmt.Errorf("主机端口范围无效，且必须与内部范围等长")
			}
			if p.Protocol == "" {
				p.Protocol = l.Protocol
			}
			if !validProtocol(p.Protocol) {
				return e, fmt.Errorf("映射协议无效")
			}
			for _, proto := range protocols(p.Protocol) {
				if !slices.Contains(protocols(l.Protocol), proto) {
					return e, fmt.Errorf("映射协议必须包含在内部监听协议中")
				}
			}
			if p.Status == "" {
				p.Status = "active"
			}
			if !validStatus(p.Status) {
				return e, fmt.Errorf("映射状态无效")
			}
			pubs = append(pubs, p)
		}
		l.Publications = pubs
		listeners = append(listeners, l)
	}
	e.Listeners = listeners
	return e, nil
}

type claim struct {
	namespace, ip, kind, protocol, label string
	start, end                           int
}

func claims(e Entry) []claim {
	host := strings.ToLower(e.Host)
	ns := "host\x00" + host
	if e.Scope == "pod" {
		ns = "pod\x00" + host + "\x00" + e.Pod
	} else if e.Scope == "container" {
		ns = "container\x00" + host + "\x00" + e.Container
	}
	out := []claim{}
	for _, l := range e.Listeners {
		if l.Status == "released" {
			continue
		}
		for _, proto := range protocols(l.Protocol) {
			out = append(out, claim{ns, l.ListenIP, e.Scope, proto, l.Service, l.PortStart, l.PortEnd})
		}
		for _, p := range l.Publications {
			if p.Status == "released" {
				continue
			}
			for _, proto := range protocols(p.Protocol) {
				out = append(out, claim{"host\x00" + host, p.HostIP, "host", proto, l.Service, p.HostStart, p.HostEnd})
			}
		}
	}
	return out
}
func overlap(a, b claim) bool {
	return a.namespace == b.namespace && a.protocol == b.protocol && a.start <= b.end && b.start <= a.end && (a.ip == b.ip || a.ip == "0.0.0.0" || b.ip == "0.0.0.0")
}
func entryLabel(e Entry) string {
	v := e.Host + " / "
	if e.Pod != "" {
		v += e.Pod + " / "
	}
	return v + e.Container
}
func entryConflicts(candidate Entry, existing []Entry) []Conflict {
	out := []Conflict{}
	ac := claims(candidate)
	add := func(a, b claim, e Entry) {
		label := entryLabel(e)
		if b.label != "" {
			label += "（" + b.label + "）"
		}
		out = append(out, Conflict{a.kind, e.ID, label, max(a.start, b.start), min(a.end, b.end), a.protocol})
	}
	for i, a := range ac {
		for _, b := range ac[:i] {
			if overlap(a, b) {
				add(a, b, candidate)
				if len(out) >= 20 {
					return out
				}
			}
		}
	}
	for _, e := range existing {
		if candidate.ID != "" && candidate.ID == e.ID {
			continue
		}
		bc := claims(e)
		for _, a := range ac {
			for _, b := range bc {
				if overlap(a, b) {
					add(a, b, e)
					if len(out) >= 20 {
						return out
					}
				}
			}
		}
	}
	return out
}
func validateEntries(entries []Entry) ([]Entry, error) {
	if len(entries) > maxEntries {
		return nil, fmt.Errorf("最多支持 %d 个容器", maxEntries)
	}
	out := make([]Entry, 0, len(entries))
	ids := map[string]bool{}
	names := map[string]bool{}
	pods := map[string][]string{}
	endpoints := 0
	for _, e := range entries {
		var err error
		e, err = normalized(e)
		if err != nil {
			return nil, err
		}
		if e.ID == "" {
			e.ID = newID()
		}
		if len(e.ID) > 120 || ids[e.ID] {
			return nil, fmt.Errorf("容器 ID 无效或重复")
		}
		ids[e.ID] = true
		key := strings.ToLower(e.Host) + "\x00" + e.Container
		if names[key] {
			return nil, fmt.Errorf("同一主机上的容器名称必须唯一，请编辑已有容器")
		}
		names[key] = true
		if e.Scope == "pod" {
			key = strings.ToLower(e.Host) + "\x00" + e.Pod
			if ns, ok := pods[key]; ok && !slices.Equal(ns, e.Networks) {
				return nil, fmt.Errorf("同一 Pod 的自定义网络必须一致")
			}
			pods[key] = e.Networks
		}
		endpoints += len(e.Listeners)
		for _, l := range e.Listeners {
			endpoints += len(l.Publications)
		}
		if endpoints > 20000 {
			return nil, fmt.Errorf("最多登记 20000 个监听和映射")
		}
		if c := entryConflicts(e, out); len(c) > 0 {
			return nil, &ConflictError{c}
		}
		if e.UpdatedAt == "" {
			e.UpdatedAt = timestamp()
		}
		out = append(out, e)
	}
	return out, nil
}

// A Pod's shared network list is updated for every member in the same transaction.
func mergedEntries(e Entry, all []Entry) ([]Entry, error) {
	var err error
	e, err = normalized(e)
	if err != nil {
		return nil, err
	}
	if found := entryConflicts(e, all); len(found) > 0 {
		return nil, &ConflictError{found}
	}
	out := make([]Entry, 0, len(all)+1)
	endpoints := 0
	for _, v := range all {
		if v.ID == e.ID {
			continue
		}
		if strings.EqualFold(v.Host, e.Host) && v.Container == e.Container {
			return nil, fmt.Errorf("同一主机上的容器名称必须唯一，请编辑已有容器")
		}
		if v.Scope == "pod" && e.Scope == "pod" && strings.EqualFold(v.Host, e.Host) && v.Pod == e.Pod {
			v.Networks = slices.Clone(e.Networks)
			v.UpdatedAt = e.UpdatedAt
		}
		out = append(out, v)
	}
	out = append(out, e)
	if len(out) > maxEntries {
		return nil, fmt.Errorf("最多支持 %d 个容器", maxEntries)
	}
	for _, v := range out {
		endpoints += len(v.Listeners)
		for _, l := range v.Listeners {
			endpoints += len(l.Publications)
		}
	}
	if endpoints > 20000 {
		return nil, fmt.Errorf("最多登记 20000 个监听和映射")
	}
	return out, nil
}
