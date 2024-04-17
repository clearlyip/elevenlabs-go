package elevenlabs

import (
	"bufio"
	"io"
	"strings"
)

type textChunk struct {
	Text                 string `json:"text"`
	TryTriggerGeneration bool   `json:"try_trigger_generation,omitempty"`
}

type streamingInputResponse struct {
	Audio string `json:"audio"`
}

// readText reads from an io.Reader and sends the text over a channel.
func readText(r io.Reader, text chan<- string) {
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanWords)

	for scanner.Scan() {
		word := scanner.Text()
		text <- word
	}

	close(text)
}

// textChunker reads chunks from a slice of strings and writes them to the provided io.Writer
func textChunker(chunks chan<- string, text <-chan string) {
	splitters := []string{".", ",", "?", "!", ";", ":", "—", "-", "(", ")", "[", "]", "}", " "}
	buffer := ""

	for text := range text {
		if endsWithAny(buffer, splitters) {
			if endsWith(buffer, " ") {
				chunks <- buffer
			} else {
				chunks <- buffer + " "
			}
			buffer = text
		} else if startsWithAny(text, splitters) {
			output := buffer + string(text[0])
			if endsWith(output, " ") {
				chunks <- output
			} else {
				chunks <- output + " "
			}
			buffer = text[1:]
		} else {
			buffer += text
		}
	}
	if buffer != "" {
		chunks <- buffer
	}

	close(chunks)
}

// endsWithAny checks if the given string ends with any of the specified substrings.
func endsWithAny(s string, subs []string) bool {
	for _, sub := range subs {
		if endsWith(s, sub) {
			return true
		}
	}
	return false
}

// startsWithAny checks if the given string starts with any of the specified substrings.
func startsWithAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.HasPrefix(s, sub) {
			return true
		}
	}
	return false
}

// endsWith checks if the given string ends with the specified substring.
func endsWith(s, sub string) bool {
	return strings.HasSuffix(s, sub)
}
