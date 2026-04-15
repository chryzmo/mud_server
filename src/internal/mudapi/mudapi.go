package mudapi

import (
	"encoding/json"
	"fmt"
	mudserver "mud_server/src/internal/mudserver"
	"net/http"
	"strconv"
)

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func HandleNodeRequest(ms *mudserver.MudServer, w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, ms.World.PrintWorldRoomsAsJSON())
	case http.MethodPost:
		handleNodeCreate(ms, w, r)
	case http.MethodPut:
		handleNodeUpdate(ms, w, r)
	case http.MethodDelete:
		handleNodeDelete(ms, w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func HandleEdgeRequest(ms *mudserver.MudServer, w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, ms.World.PrintWorldEdgesAsJSON())
}

type nodeCreateRequest struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	ExitJSON    string `json:"ExitJSON"`
}

type nodeUpdateRequest struct {
	ID          string `json:"id"`
	Name        string `json:"Name"`
	Description string `json:"Description"`
	ExitJSON    string `json:"ExitJSON"`
}

func handleNodeCreate(ms *mudserver.MudServer, w http.ResponseWriter, r *http.Request) {
	var req nodeCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	room := mudserver.NewRoom(req.Name, req.Description, req.ExitJSON)
	newID, err := room.CreateInDB(&ms.DB)
	if err != nil {
		fmt.Println("handleNodeCreate error: " + err.Error())
		http.Error(w, "Failed to create room", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int32{"id": newID})
}

func handleNodeUpdate(ms *mudserver.MudServer, w http.ResponseWriter, r *http.Request) {
	var req nodeUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	id64, err := strconv.ParseInt(req.ID, 10, 32)
	if err != nil {
		http.Error(w, "Invalid room ID", http.StatusBadRequest)
		return
	}
	roomID := int32(id64)

	if room, ok := ms.World.RoomMap[roomID]; ok {
		// Update in-memory state and re-link exits so players see changes immediately.
		room.Name = req.Name
		room.Description = req.Description
		room.ExitJSON = req.ExitJSON
		ms.World.ReLinkRoomExits(room)
		room.WriteToDB(&ms.DB)
	} else {
		// Room not loaded in memory — update DB directly without touching inventory.
		if err := mudserver.UpdateRoomMetadataInDB(&ms.DB, roomID, req.Name, req.Description, req.ExitJSON); err != nil {
			fmt.Println("handleNodeUpdate error: " + err.Error())
			http.Error(w, "Failed to update room", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func handleNodeDelete(ms *mudserver.MudServer, w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id64, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil {
		http.Error(w, "Invalid room ID", http.StatusBadRequest)
		return
	}
	roomID := int32(id64)

	room, ok := ms.World.RoomMap[roomID]
	if !ok {
		room = mudserver.NewRoom("", "", "{}")
		room.ID = roomID
	}

	if err := room.DeleteFromDB(&ms.DB); err != nil {
		fmt.Println("handleNodeDelete error: " + err.Error())
		http.Error(w, "Failed to delete room", http.StatusInternalServerError)
		return
	}

	delete(ms.World.RoomMap, roomID)
	w.WriteHeader(http.StatusOK)
}
