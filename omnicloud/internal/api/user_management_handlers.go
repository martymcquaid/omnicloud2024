package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/omnicloud/omnicloud/internal/db"
)

// --- Request / Response types ---

type userResponse struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type updateUserRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	IsActive *bool  `json:"is_active"`
}

type changePasswordRequest struct {
	Password string `json:"password"`
}

type rolePermissionResponse struct {
	Role         string   `json:"role"`
	AllowedPages []string `json:"allowed_pages"`
	Description  string   `json:"description"`
}

type updateRolePermissionsRequest struct {
	AllowedPages []string `json:"allowed_pages"`
	Description  string   `json:"description"`
}

// --- Helpers ---

// requireAdmin extracts the user from the session and verifies admin role.
// Returns the user if admin, or writes a 403 and returns nil.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) *db.User {
	token := extractBearerToken(r)
	if token == "" {
		respondError(w, http.StatusUnauthorized, "Authentication required", "")
		return nil
	}
	session, err := s.database.GetSession(token)
	if err != nil || session == nil {
		respondError(w, http.StatusUnauthorized, "Session expired", "")
		return nil
	}
	user, err := s.database.GetUserByID(session.UserID)
	if err != nil || user == nil {
		respondError(w, http.StatusUnauthorized, "User not found", "")
		return nil
	}
	if user.Role != "admin" {
		respondError(w, http.StatusForbidden, "Admin access required", "Only administrators can manage users and roles")
		return nil
	}
	return user
}

func toUserResponse(u *db.User) userResponse {
	return userResponse{
		ID:        u.ID.String(),
		Username:  u.Username,
		Role:      u.Role,
		IsActive:  u.IsActive,
		CreatedAt: u.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: u.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func parseAllowedPages(jsonStr string) []string {
	var pages []string
	json.Unmarshal([]byte(jsonStr), &pages)
	if pages == nil {
		pages = []string{}
	}
	return pages
}

// --- User CRUD Handlers ---

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	users, err := s.database.ListUsers()
	if err != nil {
		log.Printf("[Users] Error listing users: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to list users", "")
		return
	}
	var resp []userResponse
	for _, u := range users {
		resp = append(resp, toUserResponse(u))
	}
	if resp == nil {
		resp = []userResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request", "")
		return
	}
	if req.Username == "" || req.Password == "" || req.Role == "" {
		respondError(w, http.StatusBadRequest, "Missing fields", "Username, password, and role are required")
		return
	}
	if len(req.Password) < 4 {
		respondError(w, http.StatusBadRequest, "Password too short", "Password must be at least 4 characters")
		return
	}

	user, err := s.database.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		log.Printf("[Users] Error creating user: %v", err)
		respondError(w, http.StatusConflict, "Failed to create user", "Username may already exist")
		return
	}

	log.Printf("[Users] Created user '%s' with role '%s'", user.Username, user.Role)
	s.logActivity(r, "user.create", "users", "user", user.ID.String(), user.Username, fmt.Sprintf(`{"role":"%s"}`, user.Role), "success")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toUserResponse(user))
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	admin := s.requireAdmin(w, r)
	if admin == nil {
		return
	}
	vars := mux.Vars(r)
	userID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID", "")
		return
	}

	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request", "")
		return
	}
	if req.Username == "" || req.Role == "" {
		respondError(w, http.StatusBadRequest, "Missing fields", "Username and role are required")
		return
	}

	// Prevent demoting or deactivating the last admin
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	if userID == admin.ID && (req.Role != "admin" || !isActive) {
		respondError(w, http.StatusBadRequest, "Cannot demote yourself", "You cannot change your own role from admin or deactivate yourself")
		return
	}

	existingUser, _ := s.database.GetUserByID(userID)
	if existingUser != nil && existingUser.Role == "admin" && req.Role != "admin" {
		count, _ := s.database.CountActiveAdmins(userID)
		if count == 0 {
			respondError(w, http.StatusBadRequest, "Last admin", "Cannot change the role of the last admin user")
			return
		}
	}

	if err := s.database.UpdateUser(userID, req.Username, req.Role, isActive); err != nil {
		log.Printf("[Users] Error updating user %s: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update user", "")
		return
	}

	// If role changed or deactivated, invalidate their sessions
	if existingUser != nil && (existingUser.Role != req.Role || !isActive) {
		s.database.DeleteUserSessions(userID)
	}

	log.Printf("[Users] Updated user '%s' (role=%s, active=%v)", req.Username, req.Role, isActive)
	s.logActivity(r, "user.update", "users", "user", userID.String(), req.Username, fmt.Sprintf(`{"role":"%s","is_active":%v}`, req.Role, isActive), "success")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User updated"})
}

func (s *Server) handleChangeUserPassword(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	vars := mux.Vars(r)
	userID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID", "")
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request", "")
		return
	}
	if len(req.Password) < 4 {
		respondError(w, http.StatusBadRequest, "Password too short", "Password must be at least 4 characters")
		return
	}

	if err := s.database.UpdateUserPassword(userID, req.Password); err != nil {
		log.Printf("[Users] Error changing password for %s: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to change password", "")
		return
	}

	// Invalidate existing sessions (force re-login with new password)
	s.database.DeleteUserSessions(userID)

	log.Printf("[Users] Password changed for user %s", userID)
	s.logActivity(r, "user.password_change", "users", "user", userID.String(), "", "", "success")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Password changed"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	admin := s.requireAdmin(w, r)
	if admin == nil {
		return
	}
	vars := mux.Vars(r)
	userID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid user ID", "")
		return
	}

	// Prevent self-deletion
	if userID == admin.ID {
		respondError(w, http.StatusBadRequest, "Cannot delete yourself", "You cannot delete your own account")
		return
	}

	// Prevent deleting the last admin
	targetUser, _ := s.database.GetUserByID(userID)
	if targetUser != nil && targetUser.Role == "admin" {
		count, _ := s.database.CountActiveAdmins(userID)
		if count == 0 {
			respondError(w, http.StatusBadRequest, "Last admin", "Cannot delete the last admin user")
			return
		}
	}

	if err := s.database.DeleteUser(userID); err != nil {
		log.Printf("[Users] Error deleting user %s: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to delete user", "")
		return
	}

	targetName := ""
	if targetUser != nil {
		targetName = targetUser.Username
	}
	log.Printf("[Users] Deleted user %s", userID)
	s.logActivity(r, "user.delete", "users", "user", userID.String(), targetName, "", "success")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "User deleted"})
}

// --- Role Permission Handlers ---

func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	perms, err := s.database.ListRolePermissions()
	if err != nil {
		log.Printf("[Roles] Error listing roles: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to list roles", "")
		return
	}
	var resp []rolePermissionResponse
	for _, p := range perms {
		resp = append(resp, rolePermissionResponse{
			Role:         p.Role,
			AllowedPages: parseAllowedPages(p.AllowedPages),
			Description:  p.Description,
		})
	}
	if resp == nil {
		resp = []rolePermissionResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleUpdateRolePermissions(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}
	vars := mux.Vars(r)
	role := vars["role"]
	if role == "" {
		respondError(w, http.StatusBadRequest, "Missing role", "")
		return
	}

	// Prevent modifying admin permissions
	if role == "admin" {
		respondError(w, http.StatusBadRequest, "Cannot modify admin", "Admin role always has full access")
		return
	}

	var req updateRolePermissionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request", "")
		return
	}

	pagesJSON, _ := json.Marshal(req.AllowedPages)
	if err := s.database.UpdateRolePermissions(role, string(pagesJSON), req.Description); err != nil {
		log.Printf("[Roles] Error updating role %s: %v", role, err)
		respondError(w, http.StatusInternalServerError, "Failed to update role", "")
		return
	}

	log.Printf("[Roles] Updated permissions for role '%s': %v", role, req.AllowedPages)
	s.logActivity(r, "role.update", "users", "role", role, role, string(pagesJSON), "success")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Role permissions updated"})
}
