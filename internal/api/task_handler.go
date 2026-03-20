package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/patricksign/agentclaw/internal/agent"
	"github.com/patricksign/agentclaw/internal/memory"
	"github.com/rs/zerolog/log"
)

func (s *Server) HandlerTask(mux *http.ServeMux) {
	// Tasks
	mux.HandleFunc("GET /api/tasks", cors(s.listTasks))
	mux.HandleFunc("POST /api/tasks", cors(s.createTasks))
	mux.HandleFunc("GET /api/tasks/:id", cors(s.getTaskById))
	mux.HandleFunc("PATH /api/tasks/:id", cors(s.updateTaskById))
	mux.HandleFunc("GET /api/tasks/:id/logs", cors(s.getTokenLogTask))
}

// ─── Tasks ───────────────────────────────────────────────────────────────────

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := s.mem.ListTasks()
		if err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		if tasks == nil {
			tasks = []*agent.Task{}
		}
		writeJSON(w, http.StatusOK, tasks)
	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) createTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Title       string         `json:"title"`
			Description string         `json:"description"`
			AgentRole   string         `json:"agent_role"`
			Priority    agent.Priority `json:"priority"`
			DependsOn   []string       `json:"depends_on"`
			Tags        []string       `json:"tags"`
		}
		if err := readJSON(r, &req); err != nil {
			errJSON(w, http.StatusInternalServerError, "invalid JSON")
			return
		}
		if req.Title == "" || req.AgentRole == "" {
			errJSON(w, http.StatusInternalServerError, "title and agent_role required")
			return
		}
		if req.Priority == 0 {
			req.Priority = agent.PriorityNormal
		}

		task := &agent.Task{
			ID:          "T-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12],
			Title:       req.Title,
			Description: req.Description,
			AgentRole:   req.AgentRole,
			Priority:    req.Priority,
			DependsOn:   req.DependsOn,
			Tags:        req.Tags,
			Status:      agent.TaskQueued,
			CreatedAt:   time.Now(),
		}

		// Lưu vào memory
		if err := s.mem.SaveTask(task); err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Push vào queue
		s.queue.Push(task)

		// Broadcast event
		s.bus.Publish(agent.Event{
			Type:    agent.EvtTaskQueued,
			TaskID:  task.ID,
			Payload: task,
		})

		log.Info().Str("task", task.ID).Str("role", task.AgentRole).Msg("task queued")
		writeJSON(w, http.StatusCreated, task)

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET   /api/tasks/:id         — get task detail
func (s *Server) getTaskById(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		task, err := s.mem.GetTask(taskID)
		if err != nil {
			if err == sql.ErrNoRows {
				errJSON(w, http.StatusNotFound, "task not found")
			} else {
				errJSON(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		writeJSON(w, http.StatusOK, task)
	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// PATCH /api/tasks/:id         — update status
func (s *Server) updateTaskById(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch {
	case sub == "" && r.Method == http.MethodPatch:
		var req struct {
			Status agent.TaskStatus `json:"status"`
		}
		if err := readJSON(r, &req); err != nil {
			errJSON(w, http.StatusInternalServerError, "invalid JSON")
			return
		}
		switch req.Status {
		case agent.TaskPending, agent.TaskQueued, agent.TaskRunning,
			agent.TaskDone, agent.TaskFailed, agent.TaskCancelled:
			// valid
		default:
			errJSON(w, http.StatusInternalServerError, "invalid status value")
			return
		}
		if err := s.mem.UpdateTaskStatus(taskID, req.Status); err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		task, err := s.mem.GetTask(taskID)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, "task updated but could not be retrieved")
			return
		}
		s.bus.Publish(agent.Event{
			Type:    agent.EvtTaskDone,
			TaskID:  taskID,
			Payload: task,
		})
		writeJSON(w, http.StatusOK, task)

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET   /api/tasks/:id/logs    — token logs for task
func (s *Server) getTokenLogTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch {
	case sub == "logs" && r.Method == http.MethodGet:
		logs, err := s.mem.GetTokenLogs(taskID)
		if err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		if logs == nil {
			logs = []memory.TokenLog{}
		}
		writeJSON(w, http.StatusOK, logs)

	default:
		errJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
