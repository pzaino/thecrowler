// Copyright 2023 Paolo Fabio Zaino
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

// This command is a simple automation server that listens for commands to move the mouse and click or perform keyboard actions.
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	"golang.org/x/time/rate"

	"github.com/go-vgo/robotgo"
)

var (
	limiter *rate.Limiter
)

func main() {
	srvPort := flag.String("port", "3000", "port on where to listen for commands")
	srvHost := flag.String("host", "localhost", "host on where to listen for commands")
	sslMode := flag.String("sslmode", "disable", "enable or disable SSL")
	certFile := flag.String("certfile", "", "path to the SSL certificate file")
	keyFile := flag.String("keyfile", "", "path to the SSL key file")
	rateLmt := flag.String("ratelimit", "10,10", "rate limit in requests per second and burst limit")
	flag.Parse()

	host := *srvHost
	port := *srvPort
	ssl := *sslMode
	cert := *certFile
	key := *keyFile
	rtLmt := *rateLmt

	srv := &http.Server{
		Addr: host + ":" + port,

		// ReadHeaderTimeout is the amount of time allowed to read
		// request headers. The connection's read deadline is reset
		// after reading the headers and the Handler can decide what
		// is considered too slow for the body. If ReadHeaderTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		ReadHeaderTimeout: time.Duration(45) * time.Second,

		// ReadTimeout is the maximum duration for reading the entire
		// request, including the body. A zero or negative value means
		// there will be no timeout.
		//
		// Because ReadTimeout does not let Handlers make per-request
		// decisions on each request body's acceptable deadline or
		// upload rate, most users will prefer to use
		// ReadHeaderTimeout. It is valid to use them both.
		ReadTimeout: time.Duration(60) * time.Second,

		// WriteTimeout is the maximum duration before timing out
		// writes of the response. It is reset whenever a new
		// request's header is read. Like ReadTimeout, it does not
		// let Handlers make decisions on a per-request basis.
		// A zero or negative value means there will be no timeout.
		WriteTimeout: time.Duration(45) * time.Second,

		// IdleTimeout is the maximum amount of time to wait for the
		// next request when keep-alive are enabled. If IdleTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		IdleTimeout: time.Duration(45) * time.Second,
	}

	var rl, bl int
	rl, err := strconv.Atoi(strings.Split(rtLmt, ",")[0])
	if err != nil {
		rl = 10
	}
	bl, err = strconv.Atoi(strings.Split(rtLmt, ",")[1])
	if err != nil {
		bl = 10
	}
	limiter = rate.NewLimiter(rate.Limit(rl), bl)

	// Add a handler for the command endpoint
	//http.HandleFunc("/v1/rb", commandHandler)
	initAPIv1()

	// Start the server
	log.Printf("Starting server on port %s", port)
	if strings.ToLower(strings.TrimSpace(ssl)) == "enable" {
		log.Fatal(srv.ListenAndServeTLS(cert, key))
	} else {
		log.Fatal(srv.ListenAndServe())
	}
}

func initAPIv1() {
	cmdHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(commandHandler)))

	http.Handle("/v1/rb", cmdHandlerWithMiddlewares)
}

// RateLimitMiddleware is a middleware for rate limiting
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Rate limit exceeded")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeadersMiddleware adds security-related headers to responses
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add various security headers here
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")

		next.ServeHTTP(w, r)
	})
}

func commandHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Only POST method is accepted", http.StatusMethodNotAllowed)
		return
	}

	var cmd Command
	err := json.NewDecoder(r.Body).Decode(&cmd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	err = executeCommand(cmd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, err = w.Write([]byte("Command executed"))
	if err != nil {
		log.Println("Error writing response:", err)
	}
}

func executeCommand(cmd Command) error {
	switch cmd.Action {
	case "moveMouse":
		return MouseMove(cmd.X, cmd.Y)
	case "click":
		robotgo.Click()
		return nil
	case "type":
		return TypeStr(cmd.Value)
	case "keyTap":
		err := robotgo.KeyTap(cmd.Value)
		return err
	default:
		return fmt.Errorf("unknown action: %s", cmd.Action)
	}
}

// MouseMove moves the mouse to the specified coordinates simulating a human-like movement.
func MouseMove(x, y int) error {
	// Understand where the mouse is currently (compared to the target coordinates)
	currentX, currentY := robotgo.Location()
	// Calculate the distance between the current position and the target coordinates
	//distance := math.Sqrt(math.Pow(float64(x-currentX), 2) + math.Pow(float64(y-currentY), 2))

	// Randomize the speed and end velocity of the mouse movement
	s := getRandFloat(0, 1) + 0.2 // speed
	v := getRandFloat(0, 1) + 0.2 // end velocity
	fmt.Printf("Moving mouse to %d, %d with speed %f and end velocity %f\n", x, y, s, v)

	// Calculate the right coordinates to stop the mouse at around 3 quarters of the way
	var x1, y1 int
	if currentX != x && currentY != y {
		x1 = x - int(float64(x-currentX)*0.25)
		y1 = y - int(float64(y-currentY)*0.25)
		// Calculate the time to move the mouse
		//moveTime = time.Duration(distance/100) * time.Second
		// Move towards the target coordinates and stop roughly at 3 quarters of the way
		robotgo.MoveSmooth(x1, y1, s, v)
	} else {
		x1 = x
		y1 = y
		// moveTime = 0
	}
	coin := getRandInt(0, 1) == 0
	if !coin {
		steps := getRandInt(5, 10)
		pseudoCircularMovement(x1, y1, steps, s, v)
	}
	robotgo.MoveSmooth(x, y, s, v)
	return nil
}

func pseudoCircularMovement(x, y, steps int, s, v float64) {
	r := float64(getRandInt(16, 50))   // radius between 16 and 66
	p := getRandInt(5, 50)             // micro-pauses between steps
	pi := math.Pi                      // Pi constant for calculation
	clockwise := getRandInt(0, 1) == 0 // Direction of the circle

	// Calculate the starting point relative to x and y
	startX, startY := robotgo.Location()

	// Move to the starting position smoothly to avoid sudden jumps
	robotgo.MoveSmooth(x, y, s, v)
	time.Sleep(time.Duration(p) * time.Millisecond)

	// Adjust the center based on the desired radius and position
	centerX, centerY := x-int(r*math.Cos(pi/2)), y-int(r*math.Sin(pi/2))

	for i := 0; i <= steps; i++ {
		angle := 2 * pi * float64(i) / float64(steps) // Full circle

		// Adjust the angle for clockwise or counterclockwise movement
		if !clockwise {
			angle = -angle
		}

		newX := centerX + int(r*math.Cos(angle))
		newY := centerY + int(r*math.Sin(angle))

		// Move the mouse smoothly to the new coordinates
		robotgo.MoveSmooth(newX, newY, s, v)

		// Simulate human-like movement with a delay
		time.Sleep(time.Duration(p) * time.Millisecond)
	}

	// Optionally, move back to the original position smoothly
	robotgo.MoveSmooth(startX, startY, s, v)
}

// TypeStr types the specified string simulating a human-like typing
func TypeStr(str string) error {
	for _, c := range str {
		// Simulate human-like typing speed
		time.Sleep(4 + time.Duration(getRandInt(0, 700))*time.Millisecond)
		robotgo.TypeStr(string(c))
	}
	return nil
}

func getRandInt(min, max int) int {
	rangeInt := big.NewInt(int64(max - min + 1))
	// Generate a random number in [0, rangeInt)
	n, err := rand.Int(rand.Reader, rangeInt)
	if err != nil {
		return 0
	}
	iRnd := int((*n).Int64())
	return min + iRnd
}

func getRandFloat(min, max float64) float64 {
	rangeInt := big.NewInt(int64((max * 100) - (min * 100) + 1))
	// Generate a random number in [0, rangeInt)
	n, err := rand.Int(rand.Reader, rangeInt)
	if err != nil {
		return 0
	}
	fRnd := float64((*n).Int64()) / 100
	return min + fRnd
}
