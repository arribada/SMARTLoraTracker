package traccar

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"

	"github.com/arribada/LoraTracker/receiver/LoraToGPSServer/device"
)

// NewHandler creates a new alert type handler.
func NewHandler(m *device.Manager) *Handler {
	a := &Handler{
		devManager: m,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
	return a
}

// Handler is the alert type handler struct.
type Handler struct {
	httpClient *http.Client
	devManager *device.Manager
}

func (s *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, err := s.devManager.Parse(r)
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.SetPrefix("devName:" + data.Payload.DeviceName + ", msg:")
	defer log.SetPrefix("")

	if !data.Valid {
		if os.Getenv("DEBUG") == "1" {
			log.Printf("skipping data with invalid or stale gps coords, body:%+v", data)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	server, ok := r.Header["Traccarserver"]
	if !ok || len(server) != 1 {
		httpError(w, "missing or incorrect traccarServer header", http.StatusBadRequest)
		return
	}

	_, err = url.ParseRequestURI(server[0])
	if err != nil {
		httpError(w, "invalid traccarServer url format expected: http://serverNameOrIP", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("GET", server[0], nil)
	if err != nil {
		httpError(w, "creating a new request:"+err.Error(), http.StatusInternalServerError)
		return
	}

	q := req.URL.Query()
	q.Add("id", data.Payload.DevEUI.String())
	q.Add("lat", fmt.Sprintf("%g", data.Lat))
	q.Add("lon", fmt.Sprintf("%g", data.Lon))
	q.Add("snr", fmt.Sprintf("%g", data.Snr))
	q.Add("rssi", strconv.Itoa(data.Rssi))
	q.Add("speed", fmt.Sprintf("%f", data.Speed))
	for n, v := range data.Attr {
		q.Add(n, fmt.Sprintf("%v", v))

	}
	req.URL.RawQuery = q.Encode()

	res, err := s.httpClient.Do(req)
	if err != nil {
		httpError(w, "sending the  request err:"+err.Error(), http.StatusBadRequest)
		return
	}
	defer res.Body.Close()

	if res.StatusCode/100 != 2 {
		httpError(w, "unexpected response status code:"+strconv.Itoa(res.StatusCode)+" request:"+req.URL.Host+"?"+req.URL.RawQuery, http.StatusBadRequest)
		return
	}
	if os.Getenv("DEBUG") == "1" {
		body, err := ioutil.ReadAll(res.Body)
		if err != nil {
			log.Printf("reading response body err:%v", err)
		} else {
			log.Printf("reply status:%v, body:%v", res.StatusCode, string(body))
		}
	}

	log.Println("gps point created, request:", req.URL.RawQuery)
	w.WriteHeader(http.StatusOK)
}

func httpError(w http.ResponseWriter, err string, code int) {
	_, fn, line, _ := runtime.Caller(1)
	log.Printf("[error] %s:%d %v", fn, line, err)
	http.Error(w, err, code)
}
