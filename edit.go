// Copyright 2019 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rsc.io/todo/task"
)

var taskListCache struct {
	sync.Mutex
	m map[string]*task.List
}

func taskList(dir string) *task.List {
	dir = filepath.Clean(dir)

	taskListCache.Lock()
	defer taskListCache.Unlock()

	if taskListCache.m == nil {
		taskListCache.m = make(map[string]*task.List)
	}
	if list := taskListCache.m[dir]; list != nil {
		return list
	}
	list := task.OpenList(dir)
	taskListCache.m[dir] = list
	return list
}

func editTask(l *task.List, original []byte, t *task.Task) {
	updated := editText(original)
	if bytes.Equal(original, updated) {
		log.Print("no changes made")
		return
	}

	newTask, err := writeTask(l, t, updated, false)
	if err != nil {
		log.Fatal(err)
	}
	if newTask != nil {
		t = newTask
	}
}

func editText(original []byte) []byte {
	f, err := ioutil.TempFile("", "todo-edit-")
	if err != nil {
		log.Fatal(err)
	}
	if err := ioutil.WriteFile(f.Name(), original, 0600); err != nil {
		log.Fatal(err)
	}
	if err := runEditor(f.Name()); err != nil {
		log.Fatal(err)
	}
	updated, err := ioutil.ReadFile(f.Name())
	if err != nil {
		log.Fatal(err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return updated
}

func runEditor(filename string) error {
	ed := os.Getenv("VISUAL")
	if ed == "" {
		ed = os.Getenv("EDITOR")
	}
	if ed == "" {
		ed = "ed"
	}

	// If the editor contains spaces or other magic shell chars,
	// invoke it as a shell command. This lets people have
	// environment variables like "EDITOR=emacs -nw".
	// The magic list of characters and the idea of running
	// sh -c this way is taken from git/run-command.c.
	var cmd *exec.Cmd
	if strings.ContainsAny(ed, "|&;<>()$`\\\"' \t\n*?[#~=%") {
		cmd = exec.Command("sh", "-c", ed+` "$@"`, "$EDITOR", filename)
	} else {
		cmd = exec.Command(ed, filename)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("invoking editor: %v", err)
	}
	return nil
}

const bulkHeader = "\n— Bulk editing these tasks:"

func writeTask(l *task.List, old *task.Task, updated []byte, isBulk bool) (t *task.Task, err error) {
	var errbuf bytes.Buffer
	defer func() {
		if errbuf.Len() > 0 {
			err = errors.New(strings.TrimSpace(errbuf.String()))
		}
	}()

	sdata := string(updated)
	hdr := make(map[string]string)
	off := 0
	for _, line := range strings.SplitAfter(sdata, "\n") {
		off += len(line)
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		i := strings.Index(line, ":")
		if i < 0 {
			fmt.Fprintf(&errbuf, "unknown summary line: %s\n", line)
			continue
		}
		k := strings.TrimSpace(strings.ToLower(line[:i]))
		v := strings.TrimSpace(line[i+1:])
		if old == nil || old.Header(k) != v {
			hdr[k] = v
		}
	}

	if errbuf.Len() > 0 {
		return nil, nil
	}

	if old == nil && isBulk {
		// Asking to just sanity check the text parsing.
		return nil, nil
	}

	if old == nil {
		body := strings.TrimSpace(sdata[off:])
		t, err := l.Create(hdr["id"], time.Now(), hdr, []byte(body))
		if err != nil {
			fmt.Fprintf(&errbuf, "error creating task: %v\n", err)
			return nil, nil
		}
		return t, nil
	}

	marker := "\n— "
	var comment string
	if i := strings.Index(sdata, marker); i >= off {
		comment = strings.TrimSpace(sdata[off:i])
	}

	if comment == "<optional comment here>" {
		comment = ""
	}

	err = l.Write(old, time.Now(), hdr, []byte(comment))
	if err != nil {
		fmt.Fprintf(&errbuf, "error updating task: %v\n", err)
	}

	return old, nil
}

func readBulkIDs(l *task.List, text []byte) []string {
	var ids []string
	for _, line := range strings.Split(string(text), "\n") {
		id := line
		if i := strings.Index(id, "\t"); i >= 0 {
			id = id[:i]
		}
		if i := strings.Index(id, " "); i >= 0 {
			id = id[:i]
		}
		if l.Exists(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

func bulkEditStartFromText(l *task.List, content []byte) (base *task.Task, original []byte, err error) {
	ids := readBulkIDs(l, content)
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("found no todos in selection")
	}

	var all []*task.Task
	for _, id := range ids {
		var t *task.Task
		t, err = l.Read(id)
		if err == nil {
			all = append(all, t)
		}
	}
	if len(all) == 0 {
		return nil, nil, err
	}

	base, original = bulkEditStart(all)
	return base, original, nil
}

func suffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func bulkEditTasks(l *task.List, tasks []*task.Task) {
	base, original := bulkEditStart(tasks)
	updated := editText(original)
	if bytes.Equal(original, updated) {
		log.Print("no changes made")
		return
	}
	ids, err := bulkWriteTask(l, base, updated, func(s string) { log.Print(s) })
	if err != nil {
		errText := strings.Replace(err.Error(), "\n", "\t\n", -1)
		if len(ids) > 0 {
			log.Fatalf("updated %d issue%s with errors:\n\t%v", len(ids), suffix(len(ids)), errText)
		}
		log.Fatal(errText)
	}
	log.Printf("updated %d task%s", len(ids), suffix(len(ids)))
}

func bulkEditStart(tasks []*task.Task) (*task.Task, []byte) {
	c := task.Common(tasks)
	var buf bytes.Buffer
	c.PrintTo(&buf)
	fmt.Fprintf(&buf, "\n\n— Bulk editing these tasks:\n\n")
	for _, t := range tasks {
		fmt.Fprintf(&buf, "%s\t%s\n", t.ID(), t.Title())
	}
	return c, buf.Bytes()
}

func bulkWriteTask(l *task.List, base *task.Task, updated []byte, status func(string)) (ids []string, err error) {
	i := bytes.Index(updated, []byte(bulkHeader))
	if i < 0 {
		return nil, fmt.Errorf("cannot find bulk edit issue list")
	}
	ids = readBulkIDs(l, updated[i:])
	if len(ids) == 0 {
		return nil, fmt.Errorf("found no todos in bulk edit issue list")
	}

	// Check for formatting only.
	_, err = writeTask(l, nil, updated, true)
	if err != nil {
		return nil, err
	}

	// Apply to all issues in list.
	suffix := ""
	if len(ids) != 1 {
		suffix = "s"
	}
	status(fmt.Sprintf("updating %d task%s", len(ids), suffix))

	failed := false
	for _, id := range ids {
		t, err := l.Read(id)
		if err == nil {
			_, err = writeTask(l, t, updated, true)
		}
		if err != nil {
			failed = true
			status(fmt.Sprintf("writing %s: %v", id, strings.Replace(err.Error(), "\n", "\n\t", -1)))
			continue
		}
	}

	if failed {
		return ids, fmt.Errorf("failed to update all tasks")
	}
	return ids, nil
}
