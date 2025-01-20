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

// #cgo CFLAGS: -std=gnu89
// #include <stdint.h>
// #include <stdio.h>
// #include "a52.h"
// a52_state_t * state;
import "C"
import (
	"bytes"
	"encoding/binary"
	_ "image/png"
	"io"
	"log"
	"os"
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

const (
	bufferSize    = 4096
	CONVERT_LEVEL = 1
	CONVERT_BIAS  = 384
)
const (
	screenWidth  = 640
	screenHeight = 480
	sampleRate   = 48000
)

type Game struct {
	audioContext *audio.Context
	audioPlayer  *audio.Player
}

func NewGame() (*Game, error) {
	g := &Game{}

	var err error
	// Initialize audio context.
	g.audioContext = audio.NewContext(sampleRate)

	in_file, err := os.Open("/Users/jarrettkuklis/Documents/GolandProjects/audio/sample-1.ac3")
	if err != nil {
		panic(err)
	}

	C.state = C.a52_init(0)
	if C.state == nil {
		log.Fatalln("A52 init failed")
	}
	// I convert the entire thing before trying to play
	// not the best but just trying to get it to work
	var converted []byte
	es_loop(in_file, func(sample_rate C.int, flags *C.int, level *C.level_t, bias *C.sample_t) C.int {
		if sampleRate != sample_rate {
			panic(sample_rate)
		}
		*flags = A52_STEREO
		*level = CONVERT_LEVEL
		*bias = CONVERT_BIAS
		return 0
	}, func(flags C.int, samplesPtr *C.sample_t) C.int {
		if ch := flags & A52_CHANNEL_MASK; ch != A52_STEREO && ch != A52_DOLBY {
			panic(flags)
		}
		const (
			samplesPerSpeaker = 256
			numberOfSpeakers  = 2
			sizeOfSample      = 2
		)
		b := make([]byte, samplesPerSpeaker*numberOfSpeakers*sizeOfSample)
		samples := unsafe.Slice((*int32)(unsafe.Pointer(samplesPtr)), samplesPerSpeaker*numberOfSpeakers)
		num := sizeOfSample * numberOfSpeakers
		for i, s := range samples[:256] {
			binary.LittleEndian.PutUint16(b[num*i:], uint16(convert(s)))
		}
		for i, s := range samples[256 : 256*2] {
			binary.LittleEndian.PutUint16(b[num*i+sizeOfSample:], uint16(convert(s)))
		}
		converted = append(converted, b...)
		return 0
	})

	// Create an audio.Player that has one stream.
	g.audioPlayer, err = g.audioContext.NewPlayer(bytes.NewReader(converted))
	if err != nil {
		return nil, err
	}

	return g, nil
}

func convert(i int32) int16 {
	i -= 0x43C00000
	if int(i) > 32767 {
		return 32767
	}
	if int(i) < -32768 {
		return -32768
	}
	return int16(i)
}

func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeyP) {
		// As audioPlayer has one stream and remembers the playing position,
		// rewinding is needed before playing when reusing audioPlayer.
		if err := g.audioPlayer.Rewind(); err != nil {
			return err
		}

		g.audioPlayer.Play()
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.audioPlayer.IsPlaying() {
		ebitenutil.DebugPrint(screen, "Bump!")
	} else {
		ebitenutil.DebugPrint(screen, "Press P to play the wav")
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	g, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("AC3 (Ebitengine Demo)")
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}

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

const A52_MONO = 1
const A52_STEREO = 2
const A52_3F = 3
const A52_2F1R = 4
const A52_3F1R = 5
const A52_2F2R = 6
const A52_3F2R = 7
const A52_CHANNEL1 = 8
const A52_CHANNEL2 = 9
const A52_DOLBY = 10
const A52_CHANNEL_MASK = 15
const A52_ADJUST_LEVEL = 32
