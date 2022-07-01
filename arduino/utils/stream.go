// This file is part of arduino-cli.
//
// Copyright 2020 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-cli.
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

package utils

import (
	"context"
	"io"
	"time"

	"github.com/djherbis/buffer"
	"github.com/djherbis/nio/v3"
)

// FeedStreamTo creates a pipe to pass data to the writer function.
// FeedStreamTo returns the io.WriteCloser side of the pipe, on which the user can write data.
// The user must call Close() on the returned io.WriteCloser to release all the resources.
// If needed, the context can be used to detect when all the data has been processed after
// closing the writer.
func FeedStreamTo(writer func(data []byte)) (io.WriteCloser, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := nio.Pipe(buffer.New(32 * 1024))
	go func() {
		defer cancel()
		data := make([]byte, 16384)
		for {
			if n, err := r.Read(data); err == nil {
				writer(data[:n])

				// Rate limit the number of outgoing gRPC messages
				// (less messages with biggger data blocks)
				if n < len(data) {
					time.Sleep(50 * time.Millisecond)
				}
			} else {
				r.Close()
				return
			}
		}
	}()
	return w, ctx
}

// ConsumeStreamFrom creates a pipe to consume data from the reader function.
// ConsumeStreamFrom returns the io.Reader side of the pipe, which the user can use to consume the data
func ConsumeStreamFrom(reader func() ([]byte, error)) io.Reader {
	r, w := io.Pipe()
	go func() {
		for {
			if data, err := reader(); err != nil {
				if err == io.EOF {
					w.Close()
				} else {
					w.CloseWithError(err)
				}
				return
			} else if _, err := w.Write(data); err != nil {
				w.Close()
				return
			}
		}
	}()
	return r
}
