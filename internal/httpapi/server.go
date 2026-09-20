package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/user"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const maxRequestBodyBytes = 1 << 20

type UserService interface {
	Create(context.Context, user.CreateInput) (user.User, error)
	Get(context.Context, uuid.UUID) (user.User, error)
	List(context.Context, int32, int32) ([]user.User, error)
}

type Readiness interface {
	Ping(context.Context) error
}

type Server struct {
	users     UserService
	readiness Readiness
	logger    *slog.Logger
}

func NewServer(users UserService, readiness Readiness, logger *slog.Logger) *Server {
	return &Server{users: users, readiness: readiness, logger: logger}
}

func (s *Server) Healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.readiness.Ping(ctx); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", slog.Any("error", err), slog.Group("request", requestLogAttrs(r.Context())...))
		writeProblem(w, r, http.StatusServiceUnavailable, "Service unavailable", "a required dependency is unavailable", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request, params ListUsersParams) {
	limit, offset := int32(20), int32(0)
	if params.Limit != nil {
		limit = *params.Limit
	}
	if params.Offset != nil {
		offset = *params.Offset
	}

	items, err := s.users.List(r.Context(), limit, offset)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	response := UserList{Items: make([]User, 0, len(items)), Limit: int(limit), Offset: int(offset)}
	for _, item := range items {
		response.Items = append(response.Items, userResponse(item))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "Unsupported media type", "Content-Type must be application/json", nil)
		return
	}

	var body CreateUser
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := decodeJSON(r.Body, &body); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "Request entity too large", "request body must not exceed 1 MiB", nil)
			return
		}
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", err.Error(), nil)
		return
	}

	created, err := s.users.Create(r.Context(), user.CreateInput{Email: string(body.Email), Name: body.Name})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/users/"+created.ID.String())
	writeJSON(w, http.StatusCreated, userResponse(created))
}

func (s *Server) GetUser(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	found, err := s.users.Get(r.Context(), id)
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userResponse(found))
}

func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, user.ErrInvalid):
		writeProblem(w, r, http.StatusBadRequest, "Invalid request", rootDetail(err), nil)
	case errors.Is(err, user.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, "User not found", "the requested user does not exist", nil)
	case errors.Is(err, user.ErrConflict):
		writeProblem(w, r, http.StatusConflict, "User already exists", "a user with this email already exists", nil)
	default:
		s.logger.ErrorContext(r.Context(), "request failed", slog.Any("error", err), slog.Group("request", requestLogAttrs(r.Context())...))
		writeProblem(w, r, http.StatusInternalServerError, "Internal server error", "an unexpected error occurred", nil)
	}
}

func userResponse(value user.User) User {
	return User{
		Id:        value.ID,
		Email:     openapi_types.Email(value.Email),
		Name:      value.Name,
		CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt,
	}
}

func decodeJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, title, detail string, validationErrors map[string]string) {
	instance := r.URL.RequestURI()
	problemType := "about:blank"
	problem := Problem{
		Type:     problemType,
		Title:    title,
		Status:   status,
		Detail:   &detail,
		Instance: &instance,
	}
	if len(validationErrors) > 0 {
		problem.Errors = &validationErrors
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem)
}

func rootDetail(err error) string {
	message := err.Error()
	if index := strings.LastIndex(message, ": "); index >= 0 {
		return message[index+2:]
	}
	return message
}
