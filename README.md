# elevenlabs-go

![Go version](https://img.shields.io/badge/go-1.18-blue)
![License](https://img.shields.io/github/license/haguro/elevenlabs-go)
![Tests](https://github.com/haguro/elevenlabs-go/actions/workflows/tests.yml/badge.svg?branch=main&event=push)
[![codecov](https://codecov.io/gh/haguro/elevenlabs-go/branch/main/graph/badge.svg?token=UM33DSSTAG)](https://codecov.io/gh/haguro/elevenlabs-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/haguro/elevenlabs-go)](https://goreportcard.com/report/github.com/haguro/elevenlabs-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/haguro/elevenlabs-go.svg)](https://pkg.go.dev/github.com/haguro/elevenlabs-go#section-documentation)

This is a Go client library for the [ElevenLabs](https://elevenlabs.io/) voice cloning and speech
synthesis platform. It provides a basic interface for Go programs to interact with the
ElevenLabs [API](https://docs.elevenlabs.io/api-reference).

Forked from: [github.com/haguro/elevenlabs-go](github.com/haguro/elevenlabs-go)

## Installation

```bash
go get github.com/clearlyip/elevenlabs-go
```

## Websocket doInputStreamingRequest (mod Jan 2025)

### Using (Consumer side)

```go
client := elevenlabs.NewClient(d.localContext, d.config.ApiKey, 1*time.Minute)
		err := client.TextToSpeechInputStream(
			d.InputChan,  // chan string
			d.ResponseChan, // chan StreamingOutputResponse
			d.config.Voice, // Voice ID
			d.config.Model, // TTS model
			elevenlabs.TextToSpeechInputStreamingRequest{
				Text:                 " ",
				TryTriggerGeneration: true,
				VoiceSettings: &elevenlabs.VoiceSettings{
					Stability:       d.config.ElevenlabsStability,
					SimilarityBoost: d.config.ElevenlabsSimilarityBoost,
					Style:           d.config.ElevenlabsStyle,
				},
			},
			elevenlabs.OutputFormat(d.codec))

		if err != nil {
			d.logger.Errorw("🧨 ElevenLabs: Got error", err)
			continue
		}
```

### Responses

These are declared within the elevenlabs driver, for example: `elevenlabs.StreamingOutputResponse`

```go
type StreamingOutputResponse struct {
	Audio               []byte                    `json:"audio"`
	IsFinal             bool                      `json:"isFinal"`
	NormalizedAlignment StreamingAlignmentSegment `json:"normalizedAlignment"`
	Alignment           StreamingAlignmentSegment `json:"alignment"`
}

type StreamingAlignmentSegment struct {
	CharStartTimesMs []int    `json:"charStartTimesMs"`
	CharDurationsMs  []int    `json:"charDurationsMs"`
	Chars            []string `json:"chars"`
}

```

Define a channel to handle these responses:

```go
var FromElevenLabsChannel  chan elevenlabs.StreamingOutputResponse
```

Create the channel and assign it:

```go
FromElevenLabsChannel = make(chan elevenlabs.StreamingOutputResponse)
```

Watch for responses:

```go
for {
	select {
	case <-d.localContext.Done():
		d.log.Debug("ElevenLabs: Exiting output buffer handling loop via localContext.Done()")
		return
	case ttsMsg := <-FromElevenLabsChannel:
		// handle the response
}
```
