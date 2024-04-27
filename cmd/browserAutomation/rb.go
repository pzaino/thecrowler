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
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-vgo/robotgo"
)

func main() {
	srvPort := flag.String("port", "3000", "port on where to listen for commands")
	flag.Parse()

	port := *srvPort

	// Add a handler for the command endpoint
	http.HandleFunc("/v1/rb", commandHandler)

	// Start the server
	log.Printf("Starting server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
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
	s := float64(rand.Intn(2)) / 10 // speed
	v := float64(rand.Intn(2)) / 10 // end velocity

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
	coin := rand.Intn(2) == 0
	if !coin {
		steps := rand.Intn(5) + 5
		pseudoCircularMovement(x1, y1, steps, s, v)
	}
	robotgo.MoveSmooth(x, y, s, v)
	return nil
}

func pseudoCircularMovement(x, y, steps int, s, v float64) {
	r := float64(rand.Intn(51) + 16) // radius between 16 and 66
	p := rand.Intn(51)               // micro-pauses between steps
	pi := math.Pi                    // Pi constant for calculation
	clockwise := rand.Intn(2) == 0   // Direction of the circle

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
		time.Sleep(4 + time.Duration(rand.Intn(700))*time.Millisecond)
		robotgo.TypeStr(string(c))
	}
	return nil
}
