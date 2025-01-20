// Copyright 2016 Hajime Hoshi
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This is an example to implement an audio player.
// See examples/wav for a simpler example to play a sound file.

package main

import "C"
import (
	"io"
	"log"
	"os"
	"unsafe"
)

// #cgo CFLAGS: -std=gnu89
// #include <stdint.h>
// #include <stdio.h>
// #include "a52.h"
// #include "audio_out.h"
// extern void es_loop (void);
// extern FILE * in_file;
// extern void handle_args (int argc, char ** argv);
// extern ao_open_t * output_open;
// extern ao_instance_t * output;
// extern a52_state_t * state;
// extern void a52_decode_data (uint8_t * start, uint8_t * end);
// ao_instance_t * open_output (void) {
// 	return output_open();
// }
//
// int output_setup(ao_instance_t * instance, int sample_rate, int * flags, level_t * level, sample_t * bias) {
//	return output->setup (instance, sample_rate, flags, level, bias);
// }
// int output_play(ao_instance_t * instance, int flags, sample_t * samples) {
// 	return output->play (instance, flags, samples);
// }
import "C"
import (
	_ "image/png"
)

const bufferSize = 4096

func main() {
	drivers := unsafe.Slice(C.ao_drivers(), 11)
	C.output_open = drivers[0].open
	in_file, err := os.Open("sample-1.ac3")
	if err != nil {
		panic(err)
	}

	C.output = C.open_output()
	if C.output == nil {
		log.Fatalln("Cannot open output")
	}

	C.state = C.a52_init(0)
	if C.state == nil {
		log.Fatalln("A52 init failed")
	}

	es_loop(in_file, func(sample_rate C.int, flags *C.int, level *C.level_t, bias *C.sample_t) C.int {
		return C.output_setup(C.output, sample_rate, flags, level, bias)
	}, func(flags C.int, samples *C.sample_t) C.int {
		return C.output_play(C.output, flags, samples)
	})

	C.a52_free(C.state)
}

func es_loop(r io.Reader, setup setupF, play playF) {
	buffer := make([]byte, bufferSize)
	for {
		n, err := r.Read(buffer)
		if err != nil {
			panic(err)
		}
		a52_decode_data(buffer, setup, play)
		if n != 4096 {
			break
		}
	}
}

var (
	buf    [3840]byte
	bufptr = buf[:]
	bufpos = buf[7:]

	/*
	 * sample_rate and flags are global because a52_decode_data could
	 * exit between the a52_syncinfo() and the ao_setup(), and we want
	 * to have the same values when we get back !
	 */
	sample_rate C.int32_t
	flags       C.int32_t
)

type setupF func(C.int, *C.int, *C.level_t, *C.sample_t) C.int
type playF func(flags C.int, samples *C.sample_t) C.int

func a52_decode_data(start []byte, setup setupF, play playF) {
	var bit_rate C.int32_t
	var len_ int32
	for {
		len_ = int32(len(start))
		if len_ == 0 {
			break
		}
		if len_ > int32(len(bufptr)-len(bufpos)) {
			len_ = int32(len(bufptr) - len(bufpos))
		}
		copy(bufptr, start)
		bufptr = bufptr[len_:]
		start = start[len_:]
		if &bufptr[0] != &bufpos[0] {
			continue
		}
		if &bufpos[0] == &buf[7] {
			var length int32
			length = int32(C.a52_syncinfo((*C.uint8_t)(unsafe.Pointer(&buf[0])), &flags, &sample_rate, &bit_rate))
			if length == 0 {
				bufptr = buf[:]
				copy(bufptr, buf[1:6])
				continue
			}
			bufpos = buf[length:]
		} else {
			var (
				level C.level_t
				bias  C.sample_t
				i     int
			)
			if setup(sample_rate, &flags, &level, &bias) != 0 {
				goto error
			}
			flags |= A52_ADJUST_LEVEL
			if C.a52_frame(C.state, (*C.uint8_t)(unsafe.Pointer(&buf[0])), &flags, &level, bias) != 0 {
				goto error
			}
			for i = 0; i < 6; i++ {
				if C.a52_block(C.state) != 0 {
					goto error
				}
				if play(flags, C.a52_samples(C.state)) != 0 {
					log.Println("output play error")
					goto error
				}
			}
			bufptr = buf[:]
			bufpos = buf[7:]
			continue
		error:
			log.Println("error")
			bufptr = buf[:]
			bufpos = buf[7:]
		}
	}
}

const A52_ADJUST_LEVEL = 32
