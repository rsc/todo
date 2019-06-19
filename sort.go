// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"

	"rsc.io/todo/task"
)

func (w *awin) ExecSort(arg string) {
	if w.mode != modeList {
		w.acme.Err("Sort can only sort task list windows")
		return
	}
	if arg != "" {
		w.sortBy = arg
	} else if w.sortBy != "" && w.sortBy != "title" {
		w.sortBy = "title"
	} else {
		w.sortBy = "id"
	}

	rev := false
	by := w.sortBy
	if strings.HasPrefix(by, "-") {
		rev = true
		by = by[1:]
	}
	var cmp func(string, string) int
	if by == "id" {
		cmp = func(x, y string) int {
			nx := lineNumber(x)
			ny := lineNumber(y)
			switch {
			case nx < ny:
				return -1
			case nx > ny:
				return +1
			case x < y:
				return -1
			case x > y:
				return +1
			}
			return 0
		}
	} else if by == "title" || by == "" {
		cmp = func(x, y string) int { return strings.Compare(skipField(x), skipField(y)) }
	} else {
		cache := make(map[string]*task.Task)
		cachedTask := func(id string) *task.Task {
			if t, ok := cache[id]; ok {
				return t
			}
			t, _ := w.list().Read(id)
			cache[id] = t
			return t
		}
		cmp = func(x, y string) int {
			tx := cachedTask(lineID(x))
			ty := cachedTask(lineID(y))
			if tx != nil && ty != nil {
				kx := tx.Header(by)
				ky := ty.Header(by)
				if kx != ky {
					return strings.Compare(kx, ky)
				}
			} else if tx != nil || ty != nil {
				if tx == nil {
					return -1
				}
				return +1
			}
			return strings.Compare(x, y)
		}
	}
	var less func(x, y string) bool
	if rev {
		less = func(x, y string) bool { return cmp(x, y) > 0 }
	} else {
		less = func(x, y string) bool { return cmp(x, y) < 0 }
	}

	if err := w.acme.Addr("0/^[0-9a-z_\\-]+\t/,"); err != nil {
		w.acme.Err("nothing to sort")
	}
	if err := w.acme.Sort(less); err != nil {
		w.acme.Err(err.Error())
	}
	w.acme.Addr("0")
	w.acme.Ctl("dot=addr")
	w.acme.Ctl("show")
}

func lineNumber(s string) int {
	n := 0
	j := 0
	for ; j < len(s) && '0' <= s[j] && s[j] <= '9'; j++ {
		n = n*10 + int(s[j]-'0')
	}
	if j < len(s) && s[j] != ' ' && s[j] != '\t' {
		return 999999999
	}
	return n
}

func lineID(s string) string {
	i := strings.Index(s, "\t")
	if i < 0 {
		return s
	}
	return s[:i]
}

func skipField(s string) string {
	i := strings.Index(s, "\t")
	if i < 0 {
		return s
	}
	for i < len(s) && s[i+1] == '\t' {
		i++
	}
	return s[i:]
}
