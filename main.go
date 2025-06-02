package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type Message struct {
	RoomID  string `json:"room_id"`
	UserID  string `json:"user_id"`
	Type    string `json:"type"`    // "alert" or "location"
	Content string `json:"content"` // The actual message content
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
		return true
	},
}

func NewServer() *Server {
	return &Server{
		Rooms: make(map[string]*Room),
	}
}

func generateRoomID() string {
	bytes := make([]byte, 4)
	_, err := rand.Read(bytes)
	if err != nil {
		log.Println("Failed to generate room ID:", err)
		return "room-error"
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
		http.Error(w, "Password is required", http.StatusBadRequest)
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
		http.Error(w, "Failed to upgrade to WebSocket", http.StatusInternalServerError)
		return
	}

	room.Lock.Lock()
	room.Clients[userID] = conn
	room.Lock.Unlock()

	log.Printf("User %s joined room %s\n", userID, roomID)

	go func() {
		defer func() {
			room.Lock.Lock()
			delete(room.Clients, userID)
			room.Lock.Unlock()
			conn.Close()
			log.Printf("User %s disconnected from room %s\n", userID, roomID)
		}()

		for {
			_, rawMessage, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error from user %s: %v\n", userID, err)
				break
			}

			var msg Message
			if err := json.Unmarshal(rawMessage, &msg); err != nil {
				log.Printf("Invalid message from %s: %s\n", userID, string(rawMessage))
				continue
			}

			log.Printf("Message received in room %s from %s → Type: %s | Content: %s\n", msg.RoomID, msg.UserID, msg.Type, msg.Content)

			switch msg.Type {
			case "alert":
				room.Lock.Lock()
				for uid, client := range room.Clients {
					if err := client.WriteMessage(websocket.TextMessage, rawMessage); err != nil {
						log.Printf("Error sending to %s: %v. Removing client.\n", uid, err)
						client.Close()
						delete(room.Clients, uid)
					}
				}
				room.Lock.Unlock()
			case "location":
				// Only log location updates
				log.Printf("Location update from %s: %s\n", msg.UserID, msg.Content)
			default:
				log.Printf("Unknown message type from %s: %s\n", msg.UserID, msg.Type)
			}
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
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		http.Error(w, "Failed to serialize message", http.StatusInternalServerError)
		return
	}

	room.Lock.Lock()
	for uid, client := range room.Clients {
		if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("Failed to send to %s: %v\n", uid, err)
			client.Close()
			delete(room.Clients, uid)
		}
	}
	room.Lock.Unlock()

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "Notification sent")
}

func main() {
	server := NewServer()
	r := mux.NewRouter()

	r.HandleFunc("/rooms", server.CreateRoom).Methods("POST")
	r.HandleFunc("/rooms/{room_id}/join", server.JoinRoom).Methods("GET")
	r.HandleFunc("/produce-notif", server.ProduceNotification).Methods("POST")

	log.Println("Server listening on :8080")
	http.ListenAndServe(":8080", r)
}
