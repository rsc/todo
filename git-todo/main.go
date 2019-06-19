// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rsc.io/todo/task"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: git-todo [repo...]\n")
	os.Exit(2)
}

var exit = 0

func main() {
	log.SetPrefix("git-todo: ")
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
		if err != nil {
			log.Fatalf("git rev-parse --show-toplevel: %v\n%s", out, err)
		}
		args = []string{strings.TrimSpace(string(out))}
		if info, err := os.Stat(args[0]); err != nil {
			log.Fatal(err)
		} else if !info.IsDir() {
			log.Fatalf("%s: not a directory", args[0])
		}
	}

	for _, arg := range args {
		update(arg)
	}
	os.Exit(exit)
}

func update(dir string) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		log.Printf("%s: not a git root", dir)
		exit = 1
		return
	}

	if err := os.Chdir(dir); err != nil {
		log.Print(err)
		exit = 1
		return
	}

	l := task.NewList(filepath.Join(os.Getenv("HOME"), "/todo/git/", filepath.Base(dir)))

	const numField = 6
	out, err := exec.Command("git", "log", "--topo-order", "--format=format:%H%x00%B%x00%s%x00%ct%x00%an <%ae>%x00%cn <%ce>%x00", "--").CombinedOutput()
	if err != nil {
		log.Printf("%s: git log: %v\n%s", dir, err, out)
		exit = 1
		return
	}
	fields := strings.Split(string(out), "\x00")
	if len(fields) < numField {
		return // nothing pending
	}
	for i, field := range fields {
		fields[i] = strings.TrimLeft(field, "\r\n")
	}
Log:
	for i := 0; i+numField <= len(fields); i += numField {
		hash := fields[i]
		message := fields[i+1]
		subject := fields[i+2]
		unixtime, err := strconv.ParseInt(fields[i+3], 0, 64)
		if err != nil {
			log.Printf("%s: git log: invalid unix time %s", dir, fields[i+3])
			exit = 1
			return
		}
		tm := time.Unix(unixtime, 0)
		author := fields[i+4]
		committer := fields[i+5]

		// Shorten hash to 7 digits, like old-school Git.
		// We don't need perfect uniqueness: if we collide
		// with an older entry, we can let it keep the 7-digit
		// prefix and use the 8-digit prefix for the newer commit.
		// We should expect about 6 such collisions for 40k commits
		// (the current Go repo size, in June 2019),
		// and only about 37 collisions for 100k commits,
		// which we won't reach for quite a long time.
		//
		//	$ hoc
		//	func expect(digits, n) { return n * (1 - (1 - 1/16^digits)^(n-1)) }
		//	expect(7, 4e4)
		//	5.959871432077435
		//	expect(7, 1e5)
		//	37.24559263136307
		//
		var id string
		for n := 7; ; n++ {
			id = hash[:n]
			t, err := l.Read(id)
			if err != nil {
				break
			}
			if t.Header("commit") == hash {
				// Already have this commit.
				continue Log
			}
		}

		url := ""
		for _, line := range strings.Split(message, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Reviewed-on: ") {
				url = strings.TrimSpace(strings.TrimPrefix(line, "Reviewed-on:"))
			}
		}

		hdr := map[string]string{
			"title":     subject,
			"commit":    hash,
			"author":    author,
			"committer": committer,
		}
		if url != "" {
			hdr["url"] = url
		}
		body, err := exec.Command("git", "log", "-n1", "--stat", hash).CombinedOutput()
		if err != nil {
			log.Printf("%s: git log -n1 --stat %s: %v\n%s", dir, hash, err, body)
			exit = 1
			continue
		}
		body = append(body, '\n')

		diff, err := exec.Command("git", "show", hash).CombinedOutput()
		if err != nil {
			log.Printf("%s: git show %s: %v\n%s", dir, hash, err, body)
			exit = 1
			continue
		}
		if len(diff) < 32*1024 {
			i := bytes.Index(diff, []byte("\ndiff"))
			if i >= 0 {
				diff = diff[i:]
			}
			body = append(body, diff...)
		}

		_, err = l.Create(id, tm, hdr, body)
		if err != nil {
			log.Printf("%s: write task: %v", dir, err)
			exit = 1
			return
		}
	}
}
