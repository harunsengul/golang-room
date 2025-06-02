const socket = new WebSocket("ws://localhost:8080/rooms/twl-server-b138b548/join?password=harun&user_id=user33");
// Log when the connection opens
socket.onopen = () => console.log("Connected to the WebSocket server");

// Log received messages
socket.onmessage = (event) => console.log("Message received:", event.data);

// Log when the connection closes
socket.onclose = () => console.log("Disconnected from the WebSocket server");

// Log any errors
socket.onerror = (error) => console.error("WebSocket error:", error);