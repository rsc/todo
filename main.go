// Copyright 2019 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Todo is a command-line and acme client for a to-do task tracking system.

	usage: todo [-a] [-e] [-d subdir] [-done] <query>

Todo runs the query and prints the maching tasks, one per line.
If the query is a single task number, as in ``todo 1'', todo prints
the full history of the task.

The -a flag opens the task or query in an acme window.
The -e flag opens the task or query in the system editor.

The exact acme/editor integration remains undocumented
but is similar to acme mail or to rsc.io/github/issue.

*/
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"rsc.io/todo/task"
)

var (
	acmeFlag = flag.Bool("a", false, "open in new acme window")
	editFlag = flag.Bool("e", false, "edit in system editor")
	dirFlag  = flag.String("d", "", "todo subdirectory")
	doneFlag = flag.Bool("done", false, "mark matching todos as done")
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: todo [-a] [-e] <query>

If query is a single task ID, prints the full history for the task.
Otherwise, prints a table of matching results.
`)
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Parse()
	log.SetFlags(0)
	log.SetPrefix("todo: ")

	if flag.NArg() == 0 && !*acmeFlag {
		usage()
	}

	if *acmeFlag {
		runAcme()
	}

	q := strings.Join(flag.Args(), " ")
	l := taskList(*dirFlag)

	if *editFlag && q == "new" {
		editTask(l, []byte(createTemplate), nil)
		return
	}

	if t, err := l.Read(q); err == nil {
		if *editFlag {
			var buf bytes.Buffer
			issue, err := showTask(&buf, l, q)
			if err != nil {
				log.Fatal(err)
			}
			editTask(l, buf.Bytes(), issue)
			return
		}
		if *doneFlag {
			if t.Header("todo") != "done" {
				err = taskList(*dirFlag).Write(t, time.Now(), map[string]string{"todo": "done"}, nil)
				if err != nil {
					log.Print(err)
				}
			}
			return
		}
		if _, err := showTask(os.Stdout, l, q); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *editFlag {
		all, err := taskList(*dirFlag).Search(q)
		if err != nil {
			log.Fatal(err)
		}
		if len(all) == 0 {
			log.Fatal("no issues matched search")
		}
		sort.Sort(tasksByTitle(all))
		bulkEditTasks(l, all)
		return
	}

	if *doneFlag {
		all, err := taskList(*dirFlag).Search(q)
		if err != nil {
			log.Fatal(err)
		}
		for _, t := range all {
			if t.Header("todo") != "done" {
				err = taskList(*dirFlag).Write(t, time.Now(), map[string]string{"todo": "done"}, nil)
				if err != nil {
					log.Print(err)
				}
			}
		}
		return
	}

	if err := showQuery(os.Stdout, l, q); err != nil {
		log.Fatal(err)
	}
}

var createTemplate = `title: ` + `

<describe task here>

`

func showTask(w io.Writer, l *task.List, id string) (*task.Task, error) {
	t, err := l.Read(id)
	if err != nil {
		return nil, err
	}
	t.PrintTo(w)
	return t, nil
}

func showQuery(w io.Writer, l *task.List, q string) error {
	all, err := l.Search(q)
	if err != nil {
		return err
	}
	sort.Sort(tasksByTitle(all))
	for _, t := range all {
		fmt.Fprintf(w, "%v\t%v\n", t.ID(), t.Title())
	}
	return nil
}

type tasksByTitle []*task.Task

func (x tasksByTitle) Len() int      { return len(x) }
func (x tasksByTitle) Swap(i, j int) { x[i], x[j] = x[j], x[i] }
func (x tasksByTitle) Less(i, j int) bool {
	ti := x[i].Title()
	tj := x[j].Title()
	if ti != tj {
		return ti < tj
	}
	return x[i].ID() < x[j].ID()
}

type tasksByHeader struct {
	key  string
	list []*task.Task
}

func (x *tasksByHeader) Len() int      { return len(x.list) }
func (x *tasksByHeader) Swap(i, j int) { x.list[i], x.list[j] = x.list[j], x.list[i] }
func (x *tasksByHeader) Less(i, j int) bool {
	hi := x.list[i].Header(x.key)
	hj := x.list[j].Header(x.key)
	if hi != hj {
		return hi < hj
	}
	return x.list[i].ID() < x.list[j].ID()
}
