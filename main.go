package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type Message struct {
	RoomID  string `json:"room_id"`
	Message string `json:"message"`
}

type Room struct {
	Clients  map[string]*websocket.Conn
	Password string
	Lock     sync.Mutex
}

type Server struct {
	Rooms map[string]*Room
	Lock  sync.Mutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
}

func NewServer() *Server {
	return &Server{
		Rooms: make(map[string]*Room),
	}
}

func generateRoomID() string {
	bytes := make([]byte, 4) // 4 bytes for randomness
	if _, err := rand.Read(bytes); err != nil {
		panic(err) // This should not happen
	}
	return "twl-server-" + hex.EncodeToString(bytes)
}

func (s *Server) CreateRoom(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
        Password string `json:"password"`
    }

    if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
        http.Error(w, "Invalid request body", http.StatusBadRequest)
        return
    }

    if requestBody.Password == "" {
        http.Error(w, "Password is required to create a room", http.StatusBadRequest)
        return
    }

	s.Lock.Lock()
	defer s.Lock.Unlock()

	roomID := generateRoomID()
	s.Rooms[roomID] = &Room{
		Clients:  make(map[string]*websocket.Conn),
		Password: requestBody.Password, 
	}
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "Room created with ID: %s", roomID)
}

func (s *Server) JoinRoom(w http.ResponseWriter, r *http.Request) {
	roomID := mux.Vars(r)["room_id"]
	password := r.URL.Query().Get("password")
	userID := r.URL.Query().Get("user_id")

	if userID == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	s.Lock.Lock()
	room, exists := s.Rooms[roomID]
	s.Lock.Unlock()

	if !exists {
		http.Error(w, "Room does not exist", http.StatusNotFound)
		return
	}

	if room.Password != password {
		http.Error(w, "Invalid password", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "Failed to upgrade connection", http.StatusInternalServerError)
		return
	}

	// Add the client to the room with their user ID
	room.Lock.Lock()
	room.Clients[userID] = conn
	room.Lock.Unlock()

	// Start listening for messages from this client
	go func() {
		defer func() {
			// Clean up when the client disconnects
			room.Lock.Lock()
			delete(room.Clients, userID)
			room.Lock.Unlock()
			conn.Close()
		}()

		// Keep the connection open and listen for messages
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				// If there's an error (e.g., the client disconnects), break the loop
				break
			}

			// Broadcast the message to other clients in the room
			room.Lock.Lock()
			for id, client := range room.Clients {
				if id != userID {
					client.WriteMessage(messageType, message)
				}
			}
			room.Lock.Unlock()
		}
	}()
}

func (s *Server) ProduceNotification(w http.ResponseWriter, r *http.Request) {
	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.Lock.Lock()
	room, exists := s.Rooms[msg.RoomID]
	s.Lock.Unlock()

	if !exists {
		http.Error(w, "Room does not exist", http.StatusNotFound)
		return
	}

	room.Lock.Lock()
	defer room.Lock.Unlock()
	for _, client := range room.Clients {
		if err := client.WriteMessage(websocket.TextMessage, []byte(msg.Message)); err != nil {
			client.Close()
		}
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Notification sent")
}

func main() {
	server := NewServer()
	r := mux.NewRouter()
	r.HandleFunc("/rooms", server.CreateRoom).Methods("POST")
	r.HandleFunc("/rooms/{room_id}/join", server.JoinRoom).Methods("GET")
	r.HandleFunc("/produce-notif", server.ProduceNotification).Methods("POST")

	http.ListenAndServe(":8080", r)
}
