// Copyright 2019 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"
	"time"

	"9fans.net/go/acme"
	"9fans.net/go/plumb"
	"rsc.io/todo/task"
)

const root = "/todo/" // acme window root "directory"

func runAcme() {
	acme.AutoExit(true)

	q := strings.Join(flag.Args(), " ")
	if q == "" {
		q = "all"
	}

	l := taskList(".")
	if q == "new" {
		openNew(l)
	} else if look(l, q) {
		// done
	} else {
		openSearch(l, q)
	}

	go servePlumb()
	select {}
}

const (
	modeSingle = 1 + iota
	modeList
	modeCreate
	modeBulk
)

type awin struct {
	acme   *acme.Win
	name   string
	tag    string
	mode   int
	query  string
	task   *task.Task
	sortBy string // "" means "title"
}

// dir returns the window name's "directory": "/todo/home/" for /todo/home/123.
// It always ends in a slash.
func (w *awin) dir() string {
	dir, _ := path.Split(w.name)
	if dir == "" { // name has no slashes; should not happen
		dir = root
	}
	return dir
}

// adir returns the acme window directory for the list l: "/todo/home/" for taskList("home").
// It always ends in a slash.
func adir(l *task.List) string {
	return strings.TrimSuffix(path.Join(root, l.Name()), "/") + "/"
}

// list returns the task list for the window.
func (w *awin) list() *task.List {
	list, _ := path.Split(strings.TrimPrefix(w.name, root))
	if list == "" || list == "/" {
		list = "."
	}
	return taskList(list)
}

// id returns the window name's id: "123" for /todo/home/123.
func (w *awin) id() string {
	_, id := path.Split(w.name)
	return id
}

func open(w *awin) {
	var err error
	w.acme, err = acme.New()
	if err != nil {
		log.Printf("creating acme window: %v", err)
		time.Sleep(10 * time.Millisecond)
		w.acme, err = acme.New()
		if err != nil {
			log.Fatalf("creating acme window again: %v", err)
		}
	}
	w.acme.SetErrorPrefix(w.dir()) // TODO
	w.acme.Name(w.name)
	w.acme.Ctl("cleartag")
	w.acme.Fprintf("tag", " "+w.tag+" ")
	go w.ExecGet()
	go w.acme.EventLoop(w)
}

func openNew(l *task.List) {
	open(&awin{
		mode: modeCreate,
		name: adir(l) + "new",
		tag:  "Put Search",
	})
}

func openTask(l *task.List, id string) {
	open(&awin{
		mode: modeSingle,
		name: adir(l) + id,
		tag:  "Get Put Done Look",
	})
}

func openAll(l *task.List) {
	open(&awin{
		mode:  modeList,
		name:  adir(l) + "all",
		query: "all",
		tag:   "New Get Bulk Sort Search",
	})
}

func openSearch(l *task.List, query string) {
	open(&awin{
		mode:  modeList,
		name:  adir(l) + "search",
		query: query,
		tag:   "New Get Bulk Sort Search",
	})
}

func (w *awin) Execute(line string) bool {
	// Exec* methods handle all our comments.
	return false
}

func (w *awin) ExecNew() {
	openNew(w.list())
}

func (w *awin) ExecSearch(arg string) {
	if arg == "" {
		w.acme.Err("Search needs an argument")
		return
	}
	openSearch(w.list(), arg)
}

func (w *awin) ExecGet() (err error) {
	// Make long-running Get (for example, network delay)
	// easier to understand: blink during load.
	// The first blink does not happen until a second has gone by,
	// so most Gets won't see any blinking at all.
	blinkStop := w.acme.Blink()
	defer func() {
		blinkStop()
		if err != nil {
			w.acme.Ctl("dirty")
			return
		}
		w.acme.Ctl("clean")
		w.acme.Addr("0")
		w.acme.Ctl("dot=addr")
		w.acme.Ctl("show")
	}()

	switch w.mode {
	case modeCreate:
		w.acme.Clear()
		w.acme.Write("body", []byte(createTemplate))

	case modeSingle:
		var buf bytes.Buffer
		t, err := showTask(&buf, w.list(), w.id())
		if err != nil {
			return err
		}
		w.acme.Clear()
		w.acme.Write("body", buf.Bytes())
		w.task = t

	case modeList:
		var buf bytes.Buffer
		err := showQuery(&buf, w.list(), w.query)
		if err != nil {
			return err
		}
		w.acme.Clear()
		switch w.id() {
		case "search":
			w.acme.Fprintf("body", "Search %s\n\n", w.query)

		case "all":
			var buf bytes.Buffer
			for _, name := range w.list().Sublists() {
				fmt.Fprintf(&buf, "%s/\n", name)
			}
			if buf.Len() > 0 {
				fmt.Fprintf(&buf, "\n")
				w.acme.Write("body", buf.Bytes())
			}
		}
		w.acme.PrintTabbed(buf.String())

	case modeBulk:
		body, err := w.acme.ReadAll("body")
		if err != nil {
			return err
		}
		base, original, err := bulkEditStartFromText(w.list(), body)
		if err != nil {
			return err
		}
		w.acme.Clear()
		w.acme.PrintTabbed(string(original))
		w.task = base
	}
	return nil
}

func (w *awin) Look(text string) bool {
	return look(w.list(), text)
}

func look(l *task.List, text string) bool {
	// In multiline look, find all IDs.
	if strings.Contains(text, "\n") {
		for _, id := range readBulkIDs(l, []byte(text)) {
			if acme.Show(adir(l)+id) == nil {
				openTask(l, id)
			}
		}
		return true
	}

	// Otherwise, expect a single ID relative to the list,
	// which may mean switching to a different list.
	// A /todo/ prefix is OK to signal the root.
	// Do not try to handle a rooted path outside the todo hierarchy,
	// like /tmp or ../../tmp.
	var list, id string
	if strings.HasPrefix(text, "/todo/") {
		list = "."
		id = strings.TrimPrefix(id, "/todo/")
	} else if strings.HasPrefix(text, "/") {
		return false
	} else {
		full := path.Join(l.Name(), text)
		if strings.HasPrefix(full, "../") {
			return false
		}
		if task.IsList(full) {
			list = full
			id = "all"
		} else {
			list, id = path.Split(path.Join(l.Name(), text))
			if list == "" {
				list = "."
			}
		}
	}
	if !task.IsList(list) {
		return false
	}
	l = taskList(list)

	if id == "all" {
		if acme.Show(adir(l)+"all") == nil {
			openAll(l)
		}
		return true
	}
	if _, err := l.Read(id); err == nil {
		openTask(l, id)
		return true
	}
	return false
}

func (w *awin) ExecPut() {
	stop := w.acme.Blink()
	defer stop()
	switch w.mode {
	case modeSingle, modeCreate:
		old := w.task
		data, err := w.acme.ReadAll("body")
		if err != nil {
			w.acme.Err(fmt.Sprintf("Put: %v", err))
			return
		}
		t, err := writeTask(w.list(), old, data, false)
		if err != nil {
			w.acme.Err(err.Error())
			return
		}
		if w.mode == modeCreate {
			w.mode = modeSingle
			w.name = w.dir() + t.ID()
			w.acme.Name(w.name)
			w.task = t
		}
		w.ExecGet()

	case modeBulk:
		data, err := w.acme.ReadAll("body")
		if err != nil {
			w.acme.Err(fmt.Sprintf("Put: %v", err))
			return
		}
		ids, err := bulkWriteTask(w.list(), w.task, data, func(s string) { w.acme.Err("Put: " + s) })
		if err != nil {
			errText := strings.Replace(err.Error(), "\n", "\t\n", -1)
			if len(ids) > 0 {
				w.acme.Err(fmt.Sprintf("updated %d task%s with errors:\n\t%v", len(ids), suffix(len(ids)), errText))
				break
			}
			w.acme.Err(fmt.Sprintf("%s", errText))
			break
		}
		w.acme.Err(fmt.Sprintf("updated %d task%s", len(ids), suffix(len(ids))))

	case modeList:
		w.acme.Err("cannot Put task list")
	}
}

func (w *awin) ExecDel() {
	if w.mode == modeList {
		w.acme.Ctl("delete")
		return
	}
	w.acme.Ctl("del")
}

func (w *awin) ExecDebug() {
	if w.task != nil {
		w.acme.Err(fmt.Sprintf("id=%v ctime=%q mtime=%q", w.task.ID(), w.task.Header("ctime"), w.task.Header("mtime")))
	}
}

func (w *awin) ExecBulk() {
	// TODO(rsc): If Bulk has an argument, treat as search query and use results?
	if w.mode != modeList {
		w.acme.Err("can only start bulk edit in task list windows")
		return
	}
	text := w.acme.Selection()
	if text == "" {
		data, err := w.acme.ReadAll("body")
		if err != nil {
			w.acme.Err(fmt.Sprintf("%v", err))
			return
		}
		text = string(data)
	}

	open(&awin{
		name:  w.dir() + "bulkedit",
		mode:  modeBulk,
		tag:   "New Get Done Sort Search",
		query: "",
	})
}

func (w *awin) ExecDone() {
	w.putHeader("todo: done")
}

func (w *awin) ExecMute() {
	w.putHeader("todo: mute")
}

func (w *awin) ExecSnooze(arg string) {
	days := 1
	if arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil {
			w.acme.Err("Snooze needs numeric day count")
			return
		}
		days = n
	}
	wakeup := time.Now().Add(time.Duration(days) * 24 * time.Hour).Format("2006-01-02")
	w.putHeader("todo: snooze " + wakeup)
}

func (w *awin) putHeader(hdr string) bool {
	if hdr == "" {
		return true
	}
	if !strings.HasSuffix(hdr, "\n") {
		hdr += "\n"
	}
	if w.mode == modeSingle || w.mode == modeBulk {
		w.acme.Addr("0")
		w.acme.Write("data", []byte(hdr))
		w.ExecPut()
		w.acme.Ctl("del")
		return true
	}
	if w.mode == modeList {
		text := w.acme.Selection()
		if text == "" {
			return false
		}
		base, original, err := bulkEditStartFromText(w.list(), []byte(text))
		if err != nil {
			w.acme.Err(fmt.Sprintf("%v", err))
			return true
		}
		edited := append([]byte(hdr), original...)
		bulkWriteTask(w.list(), base, edited, func(s string) { w.acme.Err("Put: " + s) })
		w.acme.Ctl("addr=dot")
		w.acme.Write("data", nil)
		return true
	}
	return false
}

func servePlumb() {
	kind := strings.Trim(root, "/")
	fid, err := plumb.Open(kind, 0)
	if err != nil {
		acme.Err(root, fmt.Sprintf("plumb: %v", err))
		return
	}
	r := bufio.NewReader(fid)
	for {
		var m plumb.Message
		if err := m.Recv(r); err != nil {
			acme.Errf(root, "plumb recv: %v", err)
			return
		}
		if m.Type != "text" {
			acme.Errf(root, "plumb recv: unexpected type: %s\n", m.Type)
			continue
		}
		if m.Dst != kind {
			acme.Errf(root, "plumb recv: unexpected dst: %s\n", m.Dst)
			continue
		}
		// TODO use m.Dir?
		data := string(m.Data)
		if !strings.HasPrefix(data, root) || strings.Contains(data, "\n") {
			acme.Errf(root, "plumb recv: bad text %q", data)
			continue
		}
		if !look(taskList("."), strings.TrimPrefix(data, root)) {
			acme.Errf(root, "plumb recv: can't look %s", data)
		}
	}
}
