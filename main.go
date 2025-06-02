package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sync"
    "encoding/json"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

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
	var password struct {
		Password string `json:"password"`
	}

	err := json.NewDecoder(r.Body).Decode(&password)
	if err != nil || password.Password == "" {
		http.Error(w, "Password is required", http.StatusBadRequest)
		return
	}

	s.Lock.Lock()
	defer s.Lock.Unlock()

	roomID := generateRoomID()
	s.Rooms[roomID] = &Room{
		Clients:  make(map[string]*websocket.Conn),
		Password: password.Password,
	}

	// Hazır URL ve room bilgilerini JSON olarak dön
	joinURL := fmt.Sprintf("wss://golang-room-production.up.railway.app/rooms/%s/join?user_id=YOUR_USER_ID&password=%s", roomID, password.Password)

	response := struct {
		RoomID  string `json:"room_id"`
		JoinURL string `json:"join_url"`
	}{
		RoomID:  roomID,
		JoinURL: joinURL,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error from %s: %v", userID, err)
				break
			}

			msgText := string(message)
			log.Printf("Message from %s in room %s: %s", userID, roomID, msgText)

			// Broadcast to all clients in the room
			room.Lock.Lock()
			for uid, client := range room.Clients {
				if err := client.WriteMessage(websocket.TextMessage, message); err != nil {
					log.Printf("Send error to %s: %v (removing client)", uid, err)
					client.Close()
					delete(room.Clients, uid)
				}
			}
			room.Lock.Unlock()
		}
	}()
}

// Optional: Dummy endpoint for backward compatibility, no longer used in plain-text mode
func (s *Server) ProduceNotification(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "JSON messaging disabled. Use WebSocket for plain text.", http.StatusBadRequest)
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
