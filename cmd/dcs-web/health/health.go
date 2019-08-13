// vim:ts=4:sw=4:noexpandtab

// Health checking for sources.debian.net (and potentially other services in
// the future), so that we can reliably redirect to the service when it is
// available and fall back to our own /show if not.
package health

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Debian/dcs/cmd/dcs-web/common"
)

var status = make(chan healthRequest)

type healthRequest struct {
	service  string
	response chan bool
}

type healthUpdate struct {
	service string
	healthy bool
}

func periodically(checkFunc func() healthUpdate, updates chan healthUpdate) {
	for {
		updates <- checkFunc()
		time.Sleep(30 * time.Second)
	}
}

// health-checks sources.debian.org, run within a goroutine
func checkSDN() (update healthUpdate) {
	update.service = "sources.debian.org"

	client := &http.Client{
		Transport: &http.Transport{
			// Dials a network address with a connection timeout of 5 seconds and a data
			// deadline of 5 seconds.
			Dial: func(netw, addr string) (net.Conn, error) {
				conn, err := net.DialTimeout(netw, addr, 5*time.Second)
				if err != nil {
					return nil, err
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				return conn, nil
			},
		},
	}

	req, err := http.NewRequest("GET", "https://sources.debian.org/api/ping/", nil)
	if err != nil {
		log.Printf("health check: could not create request: %v\n", err)
		return
	}
	// We are not going to use Keep-Alive, so be upfront about it to the server.
	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("health check: sources.debian.org did not answer to HTTP\n")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("health check: sources.debian.org returned code %d\n", resp.StatusCode)
		return
	}
	type sdnStatus struct {
		Status string
	}
	status := sdnStatus{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&status); err != nil {
		log.Printf("health check: sources.debian.org returned invalid JSON: %v\n", err)
		return
	}
	if status.Status != "ok" {
		log.Printf("health check: sources.debian.org returned status == false\n")
		return
	}
	update.healthy = true
	return
}

func IsHealthy(service string) bool {
	response := make(chan bool)
	request := healthRequest{
		service:  service,
		response: response}
	status <- request
	return <-response
}

// Internally, this just starts a go routine per service that should be health-checked.
func StartChecking() {
	updates := make(chan healthUpdate)

	if *common.UseSourcesDebianNet {
		go periodically(checkSDN, updates)
	}

	// Take updates and respond to health status requests in a single
	// goroutine. It is not safe to write/read to a map from multiple go
	// routines at the same time.
	go func() {
		health := make(map[string]bool)

		for {
			select {
			case update := <-updates:
				health[update.service] = update.healthy
			case request := <-status:
				request.response <- health[request.service]
			}
		}
	}()
}
