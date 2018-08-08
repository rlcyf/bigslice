// Copyright 2018 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"

	"github.com/grailbio/base/log"
	"github.com/grailbio/bigmachine/ec2system"
	"github.com/grailbio/bigslice"
	"github.com/grailbio/bigslice/exec"
	"github.com/grailbio/bigslice/slicecmd"
	"github.com/grailbio/bigslice/sliceio"
)

var cogroupTest = bigslice.Func(func(nshard, nkey int) (slice bigslice.Slice) {
	log.Printf("cogroupTest(%d, %d)", nshard, nkey)
	// Each shard produces a (shuffled) set of values for each key.

	slice = bigslice.ReaderFunc(nshard, func(shard int, order *[]int, keys []string, values []int) (n int, err error) {
		if *order == nil {
			r := rand.New(rand.NewSource(rand.Int63()))
			*order = r.Perm(nkey)
		}
		var i int
		for i < len(*order) && i < len(keys) {
			keys[i] = fmt.Sprint((*order)[i])
			values[i] = shard<<24 | (*order)[i]
			i++
		}
		*order = (*order)[i:]
		if len(*order) == 0 {
			log.Printf("shard %d complete", shard)
			return i, sliceio.EOF
		}
		return i, nil
	})
	slice = bigslice.Cogroup(slice)
	slice = bigslice.Map(slice, func(key string, values []int) (string, []int) {
		return key, values
	})
	return
})

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: bigslicetest

Command bigslicetest runs large-scale integration testing of various
Bigslice functionality. It's distributed as a separate binary as it
requires launching external clusters, and may run for a long time.

`)
		flag.PrintDefaults()
		os.Exit(2)
	}

	var (
		nshard = flag.Int("nshard", 64, "number of shards")
		nkey   = flag.Int("nkey", 1e6, "number of keys per shard")
	)
	slicecmd.RegisterSystem("ec2", &ec2system.System{
		InstanceType: "m4.16xlarge",
	})
	slicecmd.Main(func(sess *exec.Session, args []string) error {
		ctx := context.Background()
		r, err := sess.Run(ctx, cogroupTest, *nshard, *nkey)
		if err != nil {
			return err
		}
		seen := make([]bool, *nkey)
		scan := r.Scan(ctx)
		ok := true
		errorf := func(format string, v ...interface{}) {
			log.Error.Printf(format, v...)
			ok = false
		}
		var (
			keystr string
			values []int
		)
		for scan.Scan(ctx, &keystr, &values) {
			key, err := strconv.Atoi(keystr)
			if err != nil {
				panic(err)
			}
			if seen[key] {
				errorf("saw key %v multiple times", key)
			}
			seen[key] = true
			if got, want := len(values), *nshard; got != want {
				errorf("wrong number of values for key %d: got %v, want %v", key, got, want)
			} else {
				sort.Ints(values)
				for i, v := range values {
					if got, want := v, i<<24|key; got != want {
						errorf("wrong value for key %d: got %v, want %v", got, want)
					}
				}
			}
		}
		if err := scan.Err(); err != nil {
			return err
		}
		for key, saw := range seen {
			if !saw {
				errorf("did not see key %v", key)
			}
		}
		if !ok {
			return errors.New("test errors")
		}
		return nil
	})
}
