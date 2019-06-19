// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package task

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Task struct {
	file  string
	id    string
	hdr   map[string]string
	body  []byte
	_id   []string
	ctime string
	mtime string
}

func (t *Task) ID() string    { return t.id }
func (t *Task) Title() string { return t.hdr["title"] }
func (t *Task) Header(key string) string {
	switch key {
	case "id":
		return t.id
	case "mtime":
		return t.mtime
	case "ctime":
		return t.ctime
	}
	return t.hdr[strings.ToLower(key)]
}

func (t *Task) EIDs() []string { return t._id }

type List struct {
	name     string
	dir      string
	mu       sync.Mutex
	haveAll  bool
	haveDone bool
	cache    map[string]*Task
}

var (
	emSpace = []byte("— ")
	spaceEm = []byte(" —")
	nl      = []byte("\n")
)

func isMarker(line []byte) bool {
	return bytes.HasPrefix(line, emSpace) && bytes.HasSuffix(line, spaceEm) && len(line) >= 2*len(emSpace)
}

func dir(name string) string {
	return filepath.Join(os.Getenv("HOME"), "todo", name)
}

func OpenList(name string) *List {
	return &List{name: name, dir: dir(name)}
}

func (l *List) Name() string {
	return l.name
}

func IsList(name string) bool {
	info, err := os.Stat(dir(name))
	return err == nil && info.IsDir()
}

func (l *List) Sublists() []string {
	var out []string
	infos, _ := ioutil.ReadDir(l.dir)
	for _, info := range infos {
		name := info.Name()
		if !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".") && info.IsDir() {
			out = append(out, name)
		}
	}
	return out
}

func (l *List) Exists(id string) bool {
	l.mu.Lock()
	_, ok := l.cache[id]
	l.mu.Unlock()

	if ok {
		return true
	}
	_, err1 := os.Stat(filepath.Join(l.dir, id+".todo"))
	_, err2 := os.Stat(filepath.Join(l.dir, id+".done"))
	return err1 == nil || err2 == nil
}

func (l *List) Read(id string) (*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.read(id)
}

func (l *List) read(id string) (*Task, error) {
	// l is locked
	if t := l.cache[id]; t != nil {
		return t, nil
	}
	if l.cache == nil {
		l.cache = make(map[string]*Task)
	}

	file := filepath.Join(l.dir, id+".todo")
	d, err := ioutil.ReadFile(file)
	if err != nil {
		var err1 error
		file = filepath.Join(l.dir, id+".done")
		d, err1 = ioutil.ReadFile(file)
		if err1 != nil {
			return nil, err
		}
	}

	if !bytes.HasPrefix(d, emSpace) {
		return nil, fmt.Errorf("malformed task file")
	}

	t := &Task{
		id:   id,
		file: file,
		hdr:  make(map[string]string),
		body: d,
	}
	if strings.HasSuffix(file, ".done") {
		t.hdr["done"] = "done"
	}

	hdr := false
	for _, line := range bytes.Split(d, nl) {
		if isMarker(line) {
			ts := strings.TrimSpace(string(line[len(emSpace) : len(line)-len(emSpace)]))
			if t.ctime == "" {
				t.ctime = ts
			}
			t.mtime = ts
			hdr = true
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			hdr = false
			continue
		}
		if hdr {
			i := bytes.IndexByte(line, ':')
			if i < 0 {
				hdr = false
				continue
			}
			k, v := string(bytes.ToLower(bytes.TrimSpace(line[:i]))), string(bytes.TrimSpace(line[i+1:]))
			if k == "#id" {
				t._id = append(t._id, v)
				continue
			}
			if strings.HasPrefix(k, "#") {
				continue
			}
			if v == "" {
				delete(t.hdr, k)
			} else {
				t.hdr[k] = v
			}
		}
	}

	l.cache[id] = t
	return t, nil
}

func (t *Task) Done() bool {
	switch t.Header("todo") {
	case "done", "mute":
		return true
	}
	return false
}

func (l *List) Write(t *Task, now time.Time, hdr map[string]string, comment []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.write(t, now, hdr, comment)
}

func (l *List) write(t *Task, now time.Time, hdr map[string]string, comment []byte) error {
	// l is locked
	var buf bytes.Buffer
	var keys []string
	for k := range hdr {
		keys = append(keys, k)
	}
	if t.Done() && t.Header("todo") != "mute" {
		if _, ok := hdr["todo"]; !ok {
			// Pretend "todo": "" is in hdr, to undo the "todo: done".
			// Unless task is muted.
			keys = append(keys, "todo")
		}
	}
	sort.Strings(keys)
	fmt.Fprintf(&buf, "— %s —\n", now.Local().Format("2006-01-02 15:04:05"))
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s: %s\n", k, hdr[k])
	}
	fmt.Fprintf(&buf, "\n")
	buf.Write(comment)
	if len(comment) > 0 {
		if comment[len(comment)-1] != '\n' {
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}

	f, err := os.OpenFile(t.file, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	_, err1 := f.Write(buf.Bytes())
	err2 := f.Close()
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}

	// Range keys, not hdr, to pick up todo change.
	for _, k := range keys {
		v := hdr[k]
		if v == "" {
			delete(t.hdr, k)
		} else {
			t.hdr[k] = v
		}
	}
	t.body = append(t.body, buf.Bytes()...)

	if t.Done() != strings.HasSuffix(t.file, ".done") {
		base := t.file[:strings.LastIndex(t.file, ".")]
		if t.Done() {
			if err := os.Rename(base+".todo", base+".done"); err != nil {
				return err
			}
			t.file = base + ".done"
		} else {
			if err := os.Rename(base+".done", base+".todo"); err != nil {
				return err
			}
			t.file = base + ".todo"
		}
	}

	return nil
}

func (l *List) Create(id string, now time.Time, hdr map[string]string, comment []byte) (*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var file string
	var f *os.File
	if id == "" {
		names, err := filepath.Glob(filepath.Join(l.dir, "*.*"))
		if err != nil {
			return nil, err
		}
		// TODO cache max
		max := 0
		for _, name := range names {
			if !strings.HasSuffix(name, ".todo") && !strings.HasSuffix(name, ".done") {
				continue
			}
			n, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
			if max < n {
				max = n
			}
		}
		for try := 0; ; try++ {
			id = fmt.Sprintf("%d", max+try+1)
			file = filepath.Join(l.dir, id+".todo")
			var err error
			f, err = os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
			if err != nil {
				if try >= 2 {
					return nil, err
				}
				continue
			}
			break
		}
	} else {
		for _, c := range id {
			if '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || c == '-' || c == '_' {
				continue
			}
			return nil, fmt.Errorf("invalid task name %q - must be /[0-9a-z_\\-]+/", id)
		}

		if l.cache[id] != nil {
			return nil, fmt.Errorf("task already exists")
		}
		file = filepath.Join(l.dir, id+".todo")
		var err error
		f, err = os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
		if err != nil {
			return nil, err
		}
	}
	f.Close()

	t := &Task{
		file: file,
		id:   id,
		hdr:  make(map[string]string),
	}
	l.cache[id] = t

	if err := l.write(t, now, hdr, comment); err != nil {
		os.Remove(file)
		return nil, err
	}
	return t, nil
}

func (l *List) readAll(glob string) ([]*Task, error) {
	// l is locked
	names, err := filepath.Glob(filepath.Join(l.dir, glob))
	if err != nil {
		return nil, err
	}
	var tasks []*Task
	for _, name := range names {
		t, err := l.read(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
		if err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (l *List) All() ([]*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.haveAll {
		l.readAll("*.todo")
	}

	var list []*Task
	for _, t := range l.cache {
		if !t.Done() {
			list = append(list, t)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID() < list[j].ID()
	})
	return list, nil
}

func (l *List) Done() ([]*Task, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.haveDone {
		l.readAll("*.done")
	}

	var list []*Task
	for _, t := range l.cache {
		if t.Done() {
			list = append(list, t)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID() < list[j].ID()
	})
	return list, nil
}

func (l *List) Search(q string) ([]*Task, error) {
	m, needDone, err := parseQuery(q)
	if err != nil {
		return nil, err
	}

	all, err := l.All()
	if err != nil {
		return nil, err
	}
	var done []*Task
	if needDone {
		done, err = l.Done()
		if err != nil {
			return nil, err
		}
	}

	var tasks []*Task
	for _, list := range [][]*Task{all, done} {
		for _, t := range list {
			if m(t) {
				tasks = append(tasks, t)
			}
		}
	}
	return tasks, nil
}

func parseQuery(q string) (match func(*Task) bool, needDone bool, err error) {
	var ms []func(*Task) bool
	applySnooze := true
	for _, f := range strings.Fields(q) {
		var m func(*Task) bool
		neg := false
		if strings.HasPrefix(f, "-") {
			neg = true
			f = f[1:]
		}
		if f == "all" {
			m = func(t *Task) bool { return t.Header("todo") != "done" }
		} else if i := strings.Index(f, ":"); i >= 0 {
			k := f[:i]
			v := f[i+1:]
			if k == "todo" && (strings.Contains(v, "done") || strings.Contains(v, "mute")) {
				needDone = true
			}
			if k == "todo" && strings.Contains(v, "snooze") {
				applySnooze = false
			}
			if strings.HasPrefix(v, "<") {
				m = func(t *Task) bool { return t.hdr[k] != "" && t.hdr[k] < v[1:] }
			} else if strings.HasPrefix(v, ">") {
				m = func(t *Task) bool { return t.hdr[k] != "" && t.hdr[k] > v[1:] }
			} else if strings.HasPrefix(v, "=") {
				m = func(t *Task) bool { return t.hdr[k] == v[1:] }
			} else {
				m = func(t *Task) bool { return strings.Contains(t.hdr[k], v) }
			}
		} else {
			b := []byte(f)
			m = func(t *Task) bool { return bytes.Contains(t.body, b) }
		}
		if neg {
			m1 := m
			m = func(t *Task) bool { return !m1(t) }
		}
		ms = append(ms, m)
	}

	if applySnooze {
		snoozeTime := "snooze " + time.Now().Format("2006-01-02")
		ms = append(ms, func(t *Task) bool {
			s := t.Header("todo")
			if strings.HasPrefix(s, "snooze ") && s > snoozeTime {
				return false
			}
			return true
		})
	}

	m := func(t *Task) bool {
		for _, m1 := range ms {
			if !m1(t) {
				return false
			}
		}
		return true
	}

	return m, needDone, nil
}

var nlEmSpace = []byte("\n— ")

func (t *Task) PrintTo(w io.Writer) {
	var keys []string
	for k := range t.hdr {
		if k != "title" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	if v, ok := t.hdr["title"]; ok {
		fmt.Fprintf(w, "title: %s\n", v)
	}
	for _, k := range keys {
		fmt.Fprintf(w, "%s: %s\n", k, t.hdr[k])
	}
	fmt.Fprintf(w, "\n")

	var update [][]byte
	start := 0
	for {
		i := bytes.Index(t.body[start:], nlEmSpace)
		if i < 0 {
			break
		}
		update = append(update, t.body[start:start+i+1])
		start += i + 1
	}
	update = append(update, t.body[start:])

	for i := len(update) - 1; i >= 0; i-- {
		w.Write(update[i])
	}
}

func Common(tasks []*Task) *Task {
	hdr := make(map[string]string)
	for i, t := range tasks {
		if i == 0 {
			for k, v := range t.hdr {
				hdr[k] = v
			}
		} else {
			for k, v := range hdr {
				if t.hdr[k] != v {
					delete(hdr, k)
				}
			}
		}
	}
	return &Task{hdr: hdr}
}
