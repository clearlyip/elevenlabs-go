//go:generate go run cmd/codegen/main.go

// Package elevenlabs provide an interface to interact with the Elevenlabs voice generation API in Go.
package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	elevenlabsBaseURL   = "https://api.elevenlabs.io/v1"
	elevenlabsBaseWSURL = "wss://api.elevenlabs.io/v1"
	defaultTimeout      = 30 * time.Second
	contentTypeJSON     = "application/json"
)

var (
	once          sync.Once
	defaultClient *Client
)

// QueryFunc represents the type of functions that sets certain query string to
// a given or certain value.
type QueryFunc func(*url.Values)

// Client represents an API client that can be used to make calls to the Elevenlabs API.
// The NewClient function should be used when instantiating a new Client.
//
// This library also includes a default client instance that can be used when it's more convenient or when
// only a single instance of Client will ever be used by the program. The default client's API key and timeout
// (which defaults to 30 seconds) can be modified with SetAPIKey and SetTimeout respectively, but the parent
// context is fixed and is set to context.Background().
type Client struct {
	baseURL   string
	baseWSUrl string
	apiKey    string
	timeout   time.Duration
	ctx       context.Context
}

func getDefaultClient() *Client {
	once.Do(func() {
		defaultClient = NewClient(context.Background(), "", defaultTimeout)
	})
	return defaultClient
}

// SetAPIKey sets the API key for the default client.
//
// It should be called before making any API calls with the default client if
// authentication is needed.
// The function takes a string argument which is the API key to be set.
func SetAPIKey(apiKey string) {
	getDefaultClient().apiKey = apiKey
}

// SetTimeout sets the timeout duration for the default client.
//
// It can be called if a custom timeout settings are required for API calls.
// The function takes a time.Duration argument which is the timeout to be set.
func SetTimeout(timeout time.Duration) {
	getDefaultClient().timeout = timeout
}

// NewClient creates and returns a new Client object with provided settings.
//
// It should be used to instantiate a new client with a specific API key, request timeout, and context.
//
// It takes a context.Context argument which act as the parent context to be used for requests made by this
// client, a string argument that represents the API key to be used for authenticated requests and
// a time.Duration argument that represents the timeout duration for the client's requests.
//
// It returns a pointer to a newly created Client.
func NewClient(ctx context.Context, apiKey string, reqTimeout time.Duration) *Client {
	return &Client{baseURL: elevenlabsBaseURL, baseWSUrl: elevenlabsBaseWSURL, apiKey: apiKey, timeout: reqTimeout, ctx: ctx}
}

func (c *Client) doRequest(ctx context.Context, RespBodyWriter io.Writer, method, urlStr string, bodyBuf io.Reader, contentType string, queries ...QueryFunc) error {
	dbgString := "✏️ ELEVENLABS [DEBUG] "
	errorString := "✏️ \x1b[31mELEVENLABS [ERROR]\x1b[0m "
	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var bodyBytes []byte
	if bodyBuf != nil {
		buf, err := io.ReadAll(bodyBuf)
		if err != nil {
			log.Printf(dbgString+"failed to read body for logging: %v", err)
		}
		bodyBytes = buf
		bodyBuf = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(timeoutCtx, method, urlStr, bodyBuf)
	if err != nil {
		log.Printf(dbgString+"NewRequest error: %v", err)
		return err
	}

	req.Header.Set("Accept", "*/*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.apiKey != "" {
		req.Header.Set("xi-api-key", c.apiKey)
	}

	q := req.URL.Query()
	for _, qf := range queries {
		qf(&q)
	}
	req.URL.RawQuery = q.Encode()

	dumpReq, _ := httputil.DumpRequestOut(req, true)
	log.Printf(dbgString+" >>> HTTP REQUEST >>>\n%s", string(dumpReq))
	if len(bodyBytes) > 0 {
		log.Printf(dbgString+"Request Body:\n%s", string(bodyBytes))
	}

	client := &http.Client{}
	log.Printf(dbgString+"Sending request to %s …", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		log.Printf(errorString+"client.Do error: %v", err)
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf(errorString+"reading resp.Body: %v", err)
		return err
	}

	log.Printf(dbgString+" <<< HTTP RESPONSE <<<\nStatus: %d %s\nHeaders:", resp.StatusCode, resp.Status)
	for k, vals := range resp.Header {
		log.Printf("  %s: %s", k, strings.Join(vals, ", "))
	}
	log.Printf(dbgString+" Response body:\n%s", string(respBytes))

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized:
			var apiErr APIError
			if err := json.Unmarshal(respBytes, &apiErr); err != nil {
				return fmt.Errorf("failed to unmarshal APIError: %w", err)
			}
			return &apiErr

		case http.StatusUnprocessableEntity:
			var valErr ValidationError
			if err := json.Unmarshal(respBytes, &valErr); err != nil {
				return fmt.Errorf("failed to unmarshal ValidationError: %w", err)
			}
			return &valErr

		default:
			return fmt.Errorf("unexpected HTTP status %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}

	reader := bytes.NewReader(respBytes)
	if _, err := io.Copy(RespBodyWriter, reader); err != nil {
		log.Printf(errorString+" copying response to RespBodyWriter: %v", err)
		return err
	}

	log.Printf(dbgString + " Request completed successfully")
	return nil
}

type StreamingInputResponse struct {
	Audio               string                    `json:"audio"`
	IsFinal             bool                      `json:"isFinal"`
	NormalizedAlignment StreamingAlignmentSegment `json:"normalizedAlignment"`
	Alignment           StreamingAlignmentSegment `json:"alignment"`
}

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

type WsStreamingOutputChannel chan StreamingOutputResponse

// AudioResponsePipe io.Writer,
func (c *Client) doInputStreamingRequest(ctx context.Context, TextReader chan string, ResponseChannel chan StreamingOutputResponse, AudioResponsePipe io.Writer, url string, req TextToSpeechInputStreamingRequest, contentType string, queries ...QueryFunc) error {
	driverActive := true // Driver shut down?
	driverError := false // Unexpected errors

	headers := http.Header{}
	headers.Add("Accept", "*/*")
	if contentType != "" {
		headers.Add("Content-Type", contentType)
	}
	if c.apiKey != "" {
		headers.Add("xi-api-key", c.apiKey)
	}

	u, err := neturl.Parse(url)
	if err != nil {
		return err
	}

	q := u.Query()
	for _, qf := range queries {
		qf(&q)
	}
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send initial request
	if err := conn.WriteJSON(req); err != nil {
		return err
	}

	// Input watcher
	inputCtx, inputCancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)

	// Response watching
	go func(wg *sync.WaitGroup, errCh chan<- error) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if !driverActive {
					return
				}
				var input StreamingInputResponse
				var response StreamingOutputResponse
				if err := conn.ReadJSON(&input); err != nil {
					if driverActive {
						errCh <- err
						driverError = true
						inputCancel()
					}
					return
				}

				b, err := base64.StdEncoding.DecodeString(input.Audio)
				if err != nil {
					if driverActive {
						errCh <- err
						driverError = true
						inputCancel()
					}
					return
				}
				// Send audio through the pipeline
				if _, err := AudioResponsePipe.Write(b); err != nil {
					break
				}

				// Send non-audio via the response channel
				response = StreamingOutputResponse{
					IsFinal:             input.IsFinal,
					NormalizedAlignment: input.NormalizedAlignment,
					Alignment:           input.Alignment,
				}
				ResponseChannel <- response
			}
		}
	}(&wg, errCh)

	// Input watching
InputWatcher:
	for {
		select {
		case <-inputCtx.Done():
			driverActive = false
			break InputWatcher
		case <-ctx.Done():
			driverActive = false
			break InputWatcher
		case chunk, ok := <-TextReader:
			if !ok || !driverActive {
				break InputWatcher
			}
			ch := &textChunk{Text: chunk, TryTriggerGeneration: true}
			if err := conn.WriteJSON(ch); err != nil {
				errCh <- err
				break InputWatcher
			}
		}
	}

	// Send final "" to close out TTS buffer
	if driverActive && !driverError {
		if err := conn.WriteJSON(map[string]string{"text": ""}); err != nil {
			if ctx.Err() == nil {
				errCh <- err
			}
		}
	}
	conn.Close()
	wg.Wait()

	// Errors?
	select {
	case readErr := <-errCh:
		if driverActive || driverError {
			// Only send if the driver is active or the unexpected error flag is active
			return readErr
		} else {
			return nil
		}
	default:
	}

	return nil
}

// LatencyOptimizations returns a QueryFunc that sets the http query 'optimize_streaming_latency' to
// a certain value. It is meant to be used used with TextToSpeech and TextToSpeechStream to turn
// on latency optimization.
//
// Possible values:
// 0 - default mode (no latency optimizations).
// 1 - normal latency optimizations.
// 2 - strong latency optimizations.
// 3 - max latency optimizations.
// 4 - max latency optimizations, with text normalizer turned off (best latency, but can mispronounce things like numbers or dates).
func LatencyOptimizations(value int) QueryFunc {
	return func(q *url.Values) {
		q.Add("optimize_streaming_latency", fmt.Sprint(value))
	}
}

// OutputFormat returns a QueryFunc that sets the http query 'output_format' to a certain value.
// It is meant to be used used with TextToSpeech and TextToSpeechStream to change the output format to
// a value other than the default (mp3_44100_128).
//
// Possible values:
// mp3_22050_32 - mp3 with 22.05kHz sample rate at 32kbps.
// mp3_44100_32 - mp3 with 44.1kHz sample rate at 32kbps.
// mp3_44100_64 - mp3 with 44.1kHz sample rate at 64kbps.
// mp3_44100_96 - mp3 with 44.1kHz sample rate at 96kbps.
// mp3_44100_128 - mp3 with 44.1kHz sample rate at 128kbps (default)
// mp3_44100_192 - mp3 with 44.1kHz sample rate at 192kbps (Requires subscription of Creator tier or above).
// pcm_16000 - PCM (S16LE) with 16kHz sample rate.
// pcm_22050 - PCM (S16LE) with 22.05kHz sample rate.
// pcm_24000 - PCM (S16LE) with 24kHz sample rate.
// pcm_44100 - PCM (S16LE) with 44.1kHz sample rate (Requires subscription of Independent Publisher tier or above).
// ulaw_8000 - μ-law with 8kHz sample rate. Note that this format is commonly used for Twilio audio inputs.
func OutputFormat(value string) QueryFunc {
	return func(q *url.Values) {
		q.Add("output_format", value)
	}
}

// WithSettings returns a QueryFunc that sets the http query 'with_settings' to true. It is meant to be used with
// GetVoice to include Voice setting info with the Voice metadata.
func WithSettings() QueryFunc {
	return func(q *url.Values) {
		q.Add("with_settings", "true")
	}
}

// PageSize returns a QueryFunc that sets the http query 'page_size' to a given value. It is meant to be used
// with GetHistory to set the number of elements returned in the GetHistoryResponse.History slice.
func PageSize(n int) QueryFunc {
	return func(q *url.Values) {
		q.Add("page_size", fmt.Sprint(n))
	}
}

// StartAfter returns a QueryFunc that sets the http query 'start_after_history_item_id' to a given item ID.
// It is meant to be used with GetHistory to specify which history item to start with when retrieving history.
func StartAfter(id string) QueryFunc {
	return func(q *url.Values) {
		q.Add("start_after_history_item_id", id)
	}
}

// TextToSpeech converts and returns a given text to speech audio using a certain voice.
//
// It takes a string argument that represents the ID of the voice to be used for the text to speech conversion,
// a TextToSpeechRequest argument that contain the text to be used to generate the audio alongside other settings
// and an optional list of QueryFunc 'queries' to modify the request. The QueryFunc functions relevant for this method
// are LatencyOptimizations and OutputFormat
//
// It returns a byte slice that contains mpeg encoded audio data in case of success, or an error.
func (c *Client) TextToSpeech(voiceID string, ttsReq TextToSpeechRequest, queries ...QueryFunc) ([]byte, error) {
	reqBody, err := json.Marshal(ttsReq)
	if err != nil {
		return nil, err
	}
	b := bytes.Buffer{}
	err = c.doRequest(c.ctx, &b, http.MethodPost, fmt.Sprintf("%s/text-to-speech/%s", c.baseURL, voiceID), bytes.NewBuffer(reqBody), contentTypeJSON, queries...)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// TextToSpeechStream converts and streams a given text to speech audio using a certain voice.
//
// It takes an io.Writer argument to which the streamed audio will be copied, a string argument that represents the
// ID of the voice to be used for the text to speech conversion, a TextToSpeechRequest argument that contain the text
// to be used to generate the audio alongside other settings and an optional list of QueryFunc 'queries' to modify the
// request. The QueryFunc functions relevant for this method are LatencyOptimizations and OutputFormat.
//
// It is important to set the timeout of the client to a duration large enough to maintain the desired streaming period.
//
// It returns nil if successful or an error otherwise.
func (c *Client) TextToSpeechStream(streamWriter io.Writer, voiceID string, ttsReq TextToSpeechRequest, queries ...QueryFunc) error {
	reqBody, err := json.Marshal(ttsReq)
	if err != nil {
		return err
	}

	return c.doRequest(c.ctx, streamWriter, http.MethodPost, fmt.Sprintf("%s/text-to-speech/%s/stream", c.baseURL, voiceID), bytes.NewBuffer(reqBody), contentTypeJSON, queries...)
}

// TextToSpeechInputStream converts and returns a given text to speech audio using a certain voice.
//
// It takes an io.Reader argument that contains the text to be converted to speech, an io.Writer argument to which
// the audio data will be written, a string argument that represents the ID of the voice to be used for the text to
// speech conversion, a modelID string argument that represents the ID of the model to be used for the conversion,
// a TextToSpeechInputStreamingRequest argument that contains the settings for the conversion and
// an optional list of QueryFunc 'queries' to modify the request.
func (c *Client) TextToSpeechInputStream(textReader chan string, responseChan chan StreamingOutputResponse, AudioResponsePipe io.Writer, voiceID string, modelID string, ttsReq TextToSpeechInputStreamingRequest, queries ...QueryFunc) error {
	return c.doInputStreamingRequest(c.ctx, textReader, responseChan, AudioResponsePipe, fmt.Sprintf("%s/text-to-speech/%s/stream-input?model_id=%s", c.baseWSUrl, voiceID, modelID), ttsReq, contentTypeJSON, queries...)
}

// GetModels retrieves the list of all available models.
//
// It returns a slice of Model objects or an error.
func (c *Client) GetModels() ([]Model, error) {
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/models", c.baseURL), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return nil, err
	}

	var models []Model
	if err := json.Unmarshal(b.Bytes(), &models); err != nil {
		return nil, err
	}

	return models, nil
}

// GetVoices retrieves the list of all voices available for use.
//
// It returns a slice of Voice objects or an error.
func (c *Client) GetVoices() ([]Voice, error) {
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/voices", c.baseURL), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return nil, err
	}

	var voiceResp GetVoicesResponse
	if err := json.Unmarshal(b.Bytes(), &voiceResp); err != nil {
		return nil, err
	}

	return voiceResp.Voices, nil
}

// GetDefaultVoiceSettings retrieves the default settings for voices
//
// It returns a VoiceSettings object or an error.
func (c *Client) GetDefaultVoiceSettings() (VoiceSettings, error) {
	var voiceSettings VoiceSettings
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/voices/settings/default", c.baseURL), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return VoiceSettings{}, err
	}

	if err := json.Unmarshal(b.Bytes(), &voiceSettings); err != nil {
		return VoiceSettings{}, err
	}

	return voiceSettings, nil
}

// GetVoiceSettings retrieves the settings for a specific voice.
//
// It takes a string argument that represents the ID of the voice for which the settings are retrieved.
//
// It returns a VoiceSettings object or an error.
func (c *Client) GetVoiceSettings(voiceId string) (VoiceSettings, error) {
	var voiceSettings VoiceSettings
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/voices/%s/settings", c.baseURL, voiceId), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return VoiceSettings{}, err
	}

	if err := json.Unmarshal(b.Bytes(), &voiceSettings); err != nil {
		return VoiceSettings{}, err
	}

	return voiceSettings, nil
}

// GetVoice retrieves metadata about a certain voice.
//
// It takes a string argument that represents the ID of the voice for which the metadata are retrieved
// and an optional list of QueryFunc 'queries' to modify the request. The QueryFunc relevant for this
// function is WithSettings.
//
// It returns a Voice object or an error.
func (c *Client) GetVoice(voiceId string, queries ...QueryFunc) (Voice, error) {
	var voice Voice
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/voices/%s", c.baseURL, voiceId), &bytes.Buffer{}, contentTypeJSON, queries...)
	if err != nil {
		return Voice{}, err
	}

	if err := json.Unmarshal(b.Bytes(), &voice); err != nil {
		return Voice{}, err
	}

	return voice, nil
}

// DeleteVoice deletes a voice.
//
// It takes a string argument that represents the ID of the voice to be deleted.
//
// It returns a nil if successful, or an error.
func (c *Client) DeleteVoice(voiceId string) error {
	return c.doRequest(c.ctx, &bytes.Buffer{}, http.MethodDelete, fmt.Sprintf("%s/voices/%s", c.baseURL, voiceId), &bytes.Buffer{}, contentTypeJSON)
}

// EditVoiceSettings updates the settings for a specific voice.
//
// It takes a string argument that represents the ID of the voice to which the settings to be
// updated belong, and a VoiceSettings argument that contains the new settings to be applied.
//
// It returns nil if successful or an error otherwise.
func (c *Client) EditVoiceSettings(voiceId string, settings VoiceSettings) error {
	reqBody, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	return c.doRequest(c.ctx, &bytes.Buffer{}, http.MethodPost, fmt.Sprintf("%s/voices/%s/settings/edit", c.baseURL, voiceId), bytes.NewBuffer(reqBody), contentTypeJSON)
}

// AddVoice adds a new voice to the user's VoiceLab.
//
// It takes an AddEditVoiceRequest argument that contains the information of the voice to be added.
//
// It returns the ID of the newly added voice, or an error.
func (c *Client) AddVoice(voiceReq AddEditVoiceRequest) (string, error) {
	reqBodyBuf, contentType, err := voiceReq.buildRequestBody()
	if err != nil {
		return "", err
	}
	b := bytes.Buffer{}
	err = c.doRequest(c.ctx, &b, http.MethodPost, fmt.Sprintf("%s/voices/add", c.baseURL), reqBodyBuf, contentType)
	if err != nil {
		return "", err
	}
	var voiceResp AddVoiceResponse
	if err := json.Unmarshal(b.Bytes(), &voiceResp); err != nil {
		return "", err
	}
	return voiceResp.VoiceId, nil
}

// EditVoice updates an existing voice belonging to the user.
//
// It takes a string argument that represents the ID of the voice to update,
// and an AddEditVoiceRequest argument 'voiceReq' that contains the updated information for the voice.
//
// It returns nil if successful or an error otherwise.
func (c *Client) EditVoice(voiceId string, voiceReq AddEditVoiceRequest) error {
	reqBodyBuf, contentType, err := voiceReq.buildRequestBody()
	if err != nil {
		return err
	}
	return c.doRequest(c.ctx, &bytes.Buffer{}, http.MethodPost, fmt.Sprintf("%s/voices/%s/edit", c.baseURL, voiceId), reqBodyBuf, contentType)
}

// DeleteSample deletes a sample associated with a specific voice.
//
// It takes two string arguments representing the ID of the voice to which the sample belongs
// and the ID of the sample to be deleted respectively.
//
// It returns nil if successful or an error otherwise.
func (c *Client) DeleteSample(voiceId, sampleId string) error {
	return c.doRequest(c.ctx, &bytes.Buffer{}, http.MethodDelete, fmt.Sprintf("%s/voices/%s/samples/%s", c.baseURL, voiceId, sampleId), &bytes.Buffer{}, contentTypeJSON)
}

// GetSampleAudio retrieves the audio data for a specific sample associated with a voice.
//
// It takes two string arguments representing the IDs of the voice and sample respectively.
//
// It returns a byte slice containing the audio data in case of success or an error.
func (c *Client) GetSampleAudio(voiceId, sampleId string) ([]byte, error) {
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/voices/%s/samples/%s/audio", c.baseURL, voiceId, sampleId), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// NextHistoryPageFunc represent functions that can be used to access subsequent history pages. It is
// returned by the GetHistory client method.
//
// A NextHistoryPageFunc function wraps a call to GetHistory which will subsequently return another
// NextHistoryPageFunc until all history pages are retrieved in which case nil will be returned in its place.
//
// As such, a "while"-style for loop or recursive calls to the returned NextHistoryPageFunc can be employed
// to retrieve all history in a paginated way if needed.
type NextHistoryPageFunc func(...QueryFunc) (GetHistoryResponse, NextHistoryPageFunc, error)

// GetHistory retrieves the history of all created audio and their metadata
//
// It accepts an optional list of QueryFunc 'queries' to modify the request. The QueryFunc functions
// relevant for this function are PageSize and StartAfter.
//
// It returns a GetHistoryResponse object containing the history data, a function of type NextHistoryPageFunc
// to retrieve the next page of history, and an error.
func (c *Client) GetHistory(queries ...QueryFunc) (GetHistoryResponse, NextHistoryPageFunc, error) {
	var historyResp GetHistoryResponse
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/history", c.baseURL), &bytes.Buffer{}, contentTypeJSON, queries...)
	if err != nil {
		return GetHistoryResponse{}, nil, err
	}

	if err := json.Unmarshal(b.Bytes(), &historyResp); err != nil {
		return GetHistoryResponse{}, nil, err
	}

	if !historyResp.HasMore {
		return historyResp, nil, nil
	}

	nextPageFunc := func(qf ...QueryFunc) (GetHistoryResponse, NextHistoryPageFunc, error) {
		// TODO copy to new slice to avoid unexpected issues if query changes after few calls.
		qf = append(queries, append(qf, StartAfter(historyResp.LastHistoryItemId))...)
		return c.GetHistory(qf...)
	}
	return historyResp, nextPageFunc, nil
}

// GetHistoryItem retrieves a specific history item by its ID.
//
// It takes a string argument 'representing the ID of the history item to be retrieved.
//
// It returns a HistoryItem object representing the retrieved history item, or an error.
func (c *Client) GetHistoryItem(itemId string) (HistoryItem, error) {
	var historyItem HistoryItem
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/history/%s", c.baseURL, itemId), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return HistoryItem{}, err
	}

	if err := json.Unmarshal(b.Bytes(), &historyItem); err != nil {
		return HistoryItem{}, err
	}

	return historyItem, nil
}

// DeleteHistoryItem deletes a specific history item by its ID.
//
// It takes a string argument representing the ID of the history item to be deleted.
//
// It returns nil if successful or an error otherwise.
func (c *Client) DeleteHistoryItem(itemId string) error {
	return c.doRequest(c.ctx, &bytes.Buffer{}, http.MethodDelete, fmt.Sprintf("%s/history/%s", c.baseURL, itemId), &bytes.Buffer{}, contentTypeJSON)
}

// GetHistoryItemAudio retrieves the audio data for a specific history item by its ID.
//
// It takes a string argument representing the ID of the history item for which the audio
// data is retrieved.
//
// It returns a byte slice containing the audio data or an error.
func (c *Client) GetHistoryItemAudio(itemId string) ([]byte, error) {
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/history/%s/audio", c.baseURL, itemId), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// DownloadHistoryAudio downloads the audio data for a one or more history items.
//
// It takes a DownloadHistoryRequest argument that specifies the history item(s) to download.
//
// It returns a byte slice containing the downloaded audio data. If one history item ID was provided
// the byte slice is a mpeg encoded audio file. If multiple item IDs where provided, the byte slice
// is a zip file packing the history items' audio files.
func (c *Client) DownloadHistoryAudio(dlReq DownloadHistoryRequest) ([]byte, error) {
	reqBody, err := json.Marshal(dlReq)
	if err != nil {
		return nil, err
	}

	b := bytes.Buffer{}
	err = c.doRequest(c.ctx, &b, http.MethodPost, fmt.Sprintf("%s/history/download", c.baseURL), bytes.NewBuffer(reqBody), contentTypeJSON)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// GetSubscription retrieves the subscription details for the user.
//
// It returns a Subscription object representing the subscription details, or an error.
func (c *Client) GetSubscription() (Subscription, error) {
	sub := Subscription{}
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/user/subscription", c.baseURL), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return sub, err
	}

	if err := json.Unmarshal(b.Bytes(), &sub); err != nil {
		return sub, err
	}

	return sub, nil
}

// GetUser retrieves the user information.
//
// It returns a User object representing the user details, or an error.
//
// The Subscription object returned with User will not have the invoicing details populated.
// Use GetSubscription to retrieve the user's full subscription details.
func (c *Client) GetUser() (User, error) {
	user := User{}
	b := bytes.Buffer{}
	err := c.doRequest(c.ctx, &b, http.MethodGet, fmt.Sprintf("%s/user", c.baseURL), &bytes.Buffer{}, contentTypeJSON)
	if err != nil {
		return user, err
	}

	if err := json.Unmarshal(b.Bytes(), &user); err != nil {
		return user, err
	}

	return user, nil
}
