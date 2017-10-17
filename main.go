// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Command livecaption pipes the stdin audio data to
// Google Speech API and outputs the transcript.
//
// As an example, gst-launch can be used to capture the mic input:
//
//    $ gst-launch-1.0 -v pulsesrc ! audioconvert ! audioresample ! audio/x-raw,channels=1,rate=16000 ! filesink location=/dev/stdout | livecaption
package main

import (
	"fmt"
	//"io/ioutil"
	"log"
	"os"
	"flag"

	speech "cloud.google.com/go/speech/apiv1"
	"golang.org/x/net/context"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1"

	"github.com/bwmarrin/discordgo"
	//"github.com/bwmarrin/dgvoice"
	"layeh.com/gopus"
	//wave "github.com/zenwerk/go-wave"
)

// OnError gets called by dgvoice when an error is encountered.
// By default logs to STDERR
var OnError = func(str string, err error) {
	prefix := "dgVoice: " + str

	if err != nil {
		os.Stderr.WriteString(prefix + ": " + err.Error())
	} else {
		os.Stderr.WriteString(prefix)
	}
}

var (
	stream speechpb.Speech_StreamingRecognizeClient
	speakers    map[uint32]*gopus.Decoder
    err error
    evaluating bool
    squareSequence = []int{1,4,16,25,36,49,64,81,100,121}
    discord *discordgo.Session
)

// Technically the below settings can be adjusted however that poses
// a lot of other problems that are not handled well at this time.
// These below values seem to provide the best overall performance
const (
	channels  int = 2                   // 1 for mono, 2 for stereo
	sampleRate int = 16000               // audio sampling rate
	sampleSize int = 320
)

func main() {
	var (
		Token  = flag.String("t", "", "Owner Account Token")
		GuildID   = flag.String("g", "", "Guild ID")
		ChannelID = flag.String("c", "", "Channel ID")
//		Email     = flag.String("e", "", "Discord account email.")
//		Password  = flag.String("p", "", "Discord account password.")
	)
	flag.Parse()


	fmt.Println("Connecting to Discord...")
	// Connect to Discord
	discord, err = discordgo.New(*Token)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Opening Socket...")
	// Open Websocket
	err = discord.Open()
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Joining Channel...")
	// Connect to voice channel.
	// NOTE: Setting mute to false, deaf to true.
	dgv, err := discord.ChannelVoiceJoin(*GuildID, *ChannelID, false, false)
	if err != nil {
		fmt.Println(err)
		return
	}


	fmt.Println("Connecting to Google Speech Recognition API...")
	initializeGSR()
	echo(dgv)
	
	// Close connections
	dgv.Close()
	discord.Close()

	return
}


func initializeGSR() {
	ctx := context.Background()
	client, err := speech.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}
	stream, err = client.StreamingRecognize(ctx)
	if err != nil {
		log.Fatal(err)
	}
	evaluating = false
		// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Encoding:        speechpb.RecognitionConfig_LINEAR16,
					SampleRateHertz: 16000,
					LanguageCode:    "en-US",
					SpeechContexts: []*speechpb.SpeechContext{
						&speechpb.SpeechContext{
							Phrases: []string{"hello"},
						},
					},
				},
				InterimResults: true,
				SingleUtterance: true,
			},

		},
	}); err != nil {
		log.Fatal(err)
	}

}

func retrieveGSRResult() {
	evaluating = true
	defer stream.CloseSend()
	defer initializeGSR()

	fmt.Println("Waiting for new response.")
	for {
		resp, err := stream.Recv()
		if err != nil {
			log.Fatalf("Cannot stream results: %v", err)
		}
		if err := resp.Error; err != nil {
			log.Fatalf("Could not recognize: %v", err)
		}
		for _, result := range resp.Results {
			fmt.Printf("Result: %+v\n", result)
			if result.IsFinal {
				fmt.Println("Done retrieving.")
				
				
				evaluating = false
				return
			}
		}
	}
}


// Takes inbound audio and sends it right back out.
func echo(v *discordgo.VoiceConnection) {

	recv := make(chan *discordgo.Packet, 2)
	go Decode(v, recv)

	// v.Speaking(true)
	// defer v.Speaking(false)

	// f, err := os.OpenFile("test.raw", os.O_APPEND|os.O_WRONLY, 0600)
	// if err != nil {
 //    	panic(err)
	// }
	// defer f.Close()

	go retrieveGSRResult()

	for {
		p, ok := <-recv
		if !ok {
			return
		}

		var (
			sum int16 = 0
		)

		for i := 0; i < len(squareSequence); i++ {
			sum -= p.PCM[squareSequence[i]]
		}

        if !evaluating {
			return
			
		}


		if sum == 0 {
			continue
		}

		fmt.Println("Buffering...")

		buf := make([]byte,channels*sampleSize)

		for i := 0; i < len(p.PCM); i++ {
			var h uint8 = uint8(p.PCM[i]>>8)
			buf[i] = h
		}

		// _,err := f.Write(buf)
	 //    if err != nil {
	 //        log.Fatal(err)
	 //    }

		if err = stream.Send(&speechpb.StreamingRecognizeRequest{
			StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
				AudioContent: buf,
			},
		}); err != nil {
            log.Printf("Could not send audio: %v", err)
        }
	}
}

// ReceivePCM will receive on the the Discordgo OpusRecv channel and decode
// the opus audio into PCM then send it on the provided channel.
func Decode(v *discordgo.VoiceConnection, c chan *discordgo.Packet) {
	if c == nil {
		return
	}

	for {

		if v.Ready == false || v.OpusRecv == nil {
			OnError(fmt.Sprintf("Discordgo not to receive opus packets. %+v : %+v", v.Ready, v.OpusSend), nil)
			return
		}
		
		p, ok := <-v.OpusRecv
		if !ok {
			return
		}

		if speakers == nil {
			speakers = make(map[uint32]*gopus.Decoder)
		}

		_, ok = speakers[p.SSRC]
		if !ok {
			speakers[p.SSRC], err = gopus.NewDecoder(sampleRate, channels)
			if err != nil {
				OnError("error creating opus decoder", err)
				continue
			}
		}

		p.PCM, err = speakers[p.SSRC].Decode(p.Opus, sampleSize, false)
		if err != nil {
			OnError("Error decoding opus data", err)
			continue
		}

		c <- p
	}
}