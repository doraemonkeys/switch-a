package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
)

func TestListGroups(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["g1"] = &model.Group{ID: "g1", Name: "Group 1"}
	st.groups["g2"] = &model.Group{ID: "g2", Name: "Group 2"}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups", nil)
	w := httptest.NewRecorder()

	h.ListGroups(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var groups []model.Group
	if err := json.NewDecoder(w.Body).Decode(&groups); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(groups) != 2 {
		t.Errorf("len(groups) = %d, want 2", len(groups))
	}
}

func TestListGroups_Error(t *testing.T) {
	h, st, _ := testHandler()
	st.listErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups", nil)
	w := httptest.NewRecorder()

	h.ListGroups(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDeleteGroup_RoutingPolicyConflict(t *testing.T) {
	h, st, _ := testHandler()
	st.groups["g1"] = &model.Group{ID: "g1", Name: "Group 1"}
	st.deleteErr = &store.RoutingPolicyGroupReferenceConflictError{
		GroupID:  "g1",
		PolicyID: 5,
		Key:      model.NewRoutingPolicyNaturalKey("claude", model.RoutingPolicyModelMatchTypeNone, ""),
	}

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/g1", nil)
	setPathValue(req, "id", "g1")
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestGetGroup(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test-group"] = &model.Group{ID: "test-group", Name: "Test Group"}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups/test-group", nil)
	setPathValue(req, "id", "test-group")
	w := httptest.NewRecorder()

	h.GetGroup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var group model.Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if group.ID != "test-group" {
		t.Errorf("ID = %q, want %q", group.ID, "test-group")
	}
}

func TestGetGroup_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups/non-existent", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.GetGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetGroup_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups/", nil)
	w := httptest.NewRecorder()

	h.GetGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetGroup_InternalError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodGet, "/admin/api/groups/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.GetGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateGroup(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)

	body := `{
		"id": "new-group",
		"name": "New Group",
		"strategy": "weight"
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	if _, ok := st.groups["new-group"]; !ok {
		t.Error("group was not created in store")
	}

	if st.groups["new-group"].Strategy != "weight" {
		t.Errorf("Strategy = %q, want %q", st.groups["new-group"].Strategy, "weight")
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestCreateGroup_ValidationErrors(t *testing.T) {
	h, _, _ := testHandler()

	tests := []struct {
		name string
		body string
	}{
		{"missing id", `{"name": "Test"}`},
		{"missing name", `{"id": "test"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.CreateGroup(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestCreateGroup_InvalidJSON(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString("invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateGroup_Conflict(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["existing"] = &model.Group{ID: "existing", Name: "Existing"}

	body := `{"id": "existing", "name": "New Group"}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestCreateGroup_Defaults(t *testing.T) {
	h, st, _ := testHandler()

	body := `{"id": "new-group", "name": "New Group"}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	group := st.groups["new-group"]
	if group.Strategy != "priority" {
		t.Errorf("Strategy = %q, want %q", group.Strategy, "priority")
	}
	if group.Weight != 1 {
		t.Errorf("Weight = %d, want 1", group.Weight)
	}
	if !group.Enabled {
		t.Error("Enabled should be true by default")
	}
}

func TestCreateGroup_CreateError(t *testing.T) {
	h, st, _ := testHandler()
	st.createErr = errors.New("database error")

	body := `{"id": "new-group", "name": "New Group"}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateGroup_GetCheckError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	body := `{"id": "new-group", "name": "New Group"}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestCreateGroup_WithEnabledFalse(t *testing.T) {
	h, st, _ := testHandler()

	body := `{"id": "new-group", "name": "New Group", "enabled": false}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CreateGroup(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	if st.groups["new-group"].Enabled {
		t.Error("group should be disabled")
	}
}

func TestUpdateGroup(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)

	st.groups["test-group"] = &model.Group{ID: "test-group", Name: "Old Name", Strategy: "priority"}

	body := `{"name": "New Name", "strategy": "weight"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test-group", bytes.NewBufferString(body))
	setPathValue(req, "id", "test-group")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	updated := st.groups["test-group"]
	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
	if updated.Strategy != "weight" {
		t.Errorf("Strategy = %q, want %q", updated.Strategy, "weight")
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestUpdateGroup_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/non-existent", bytes.NewBufferString(body))
	setPathValue(req, "id", "non-existent")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateGroup_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateGroup_InvalidJSON(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{ID: "test", Name: "Test"}

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test", bytes.NewBufferString("invalid"))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateGroup_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{ID: "test", Name: "Test"}
	st.updateErr = errors.New("database error")

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateGroup_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	body := `{"name": "New Name"}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestUpdateGroup_AllFields(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{
		ID:       "test",
		Name:     "Old Name",
		Strategy: "priority",
		Priority: 0,
		Weight:   1,
		Enabled:  true,
	}

	body := `{
		"name": "New Name",
		"strategy": "weight",
		"priority": 5,
		"weight": 10,
		"enabled": false
	}`

	req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test", bytes.NewBufferString(body))
	setPathValue(req, "id", "test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.UpdateGroup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	updated := st.groups["test"]
	if updated.Name != "New Name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New Name")
	}
	if updated.Strategy != "weight" {
		t.Errorf("Strategy = %q, want %q", updated.Strategy, "weight")
	}
	if updated.Priority != 5 {
		t.Errorf("Priority = %d, want 5", updated.Priority)
	}
	if updated.Weight != 10 {
		t.Errorf("Weight = %d, want 10", updated.Weight)
	}
	if updated.Enabled {
		t.Error("Enabled should be false")
	}
}

func TestUpdateGroup_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "empty name",
			body:    `{"name": ""}`,
			wantMsg: "Name cannot be empty",
		},
		{
			name:    "zero weight",
			body:    `{"weight": 0}`,
			wantMsg: "Weight must be positive",
		},
		{
			name:    "negative weight",
			body:    `{"weight": -1}`,
			wantMsg: "Weight must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := testHandler()
			st.groups["test"] = &model.Group{ID: "test", Name: "Test", Weight: 1}

			req := httptest.NewRequest(http.MethodPut, "/admin/api/groups/test", bytes.NewBufferString(tt.body))
			setPathValue(req, "id", "test")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.UpdateGroup(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body: %s", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.wantMsg)) {
				t.Errorf("body = %s, want to contain %q", w.Body.String(), tt.wantMsg)
			}
		})
	}
}

func TestDeleteGroup(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)

	st.groups["test-group"] = &model.Group{ID: "test-group", Name: "Test"}

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/test-group", nil)
	setPathValue(req, "id", "test-group")
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if _, ok := st.groups["test-group"]; ok {
		t.Error("group was not deleted")
	}
	if lifecycles.allRetirements != 1 {
		t.Fatalf("global lifecycle retirements = %d, want 1", lifecycles.allRetirements)
	}
}

func TestDeleteGroup_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/non-existent", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteGroup_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/", nil)
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDeleteGroup_DeleteError(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{ID: "test", Name: "Test"}
	st.deleteErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDeleteGroup_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodDelete, "/admin/api/groups/test", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DeleteGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestEnableGroup(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test-group"] = &model.Group{ID: "test-group", Name: "Test Group", Enabled: false}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test-group/enable", nil)
	setPathValue(req, "id", "test-group")
	w := httptest.NewRecorder()

	h.EnableGroup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var group model.Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !group.Enabled {
		t.Error("group should be enabled")
	}

	if !st.groups["test-group"].Enabled {
		t.Error("group in store should be enabled")
	}
}

func TestSetGroupEnabled_DoesNotChangeProviderState(t *testing.T) {
	testCases := []struct {
		name                string
		path                string
		initialGroupEnabled bool
		initialProvEnabled  bool
		wantGroupEnabled    bool
		invoke              func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			name:                "enable keeps provider disabled",
			path:                "/admin/api/groups/test-group/enable",
			initialGroupEnabled: false,
			initialProvEnabled:  false,
			wantGroupEnabled:    true,
			invoke:              (*Handler).EnableGroup,
		},
		{
			name:                "disable keeps provider enabled",
			path:                "/admin/api/groups/test-group/disable",
			initialGroupEnabled: true,
			initialProvEnabled:  true,
			wantGroupEnabled:    false,
			invoke:              (*Handler).DisableGroup,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := testHandler()
			groupID := "test-group"

			st.groups[groupID] = &model.Group{
				ID:      groupID,
				Name:    "Test Group",
				Enabled: tc.initialGroupEnabled,
			}
			st.providers["test-provider"] = &model.Provider{
				ID:      "test-provider",
				Name:    "Test Provider",
				GroupID: &groupID,
				Enabled: tc.initialProvEnabled,
			}

			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			setPathValue(req, "id", groupID)
			w := httptest.NewRecorder()

			tc.invoke(h, w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
			}
			if st.groups[groupID].Enabled != tc.wantGroupEnabled {
				t.Fatalf("group enabled = %v, want %v", st.groups[groupID].Enabled, tc.wantGroupEnabled)
			}
			if st.providers["test-provider"].Enabled != tc.initialProvEnabled {
				t.Fatalf("provider enabled = %v, want %v", st.providers["test-provider"].Enabled, tc.initialProvEnabled)
			}
		})
	}
}

func TestEnableGroup_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/non-existent/enable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.EnableGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestEnableGroup_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups//enable", nil)
	w := httptest.NewRecorder()

	h.EnableGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEnableGroup_GetError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestEnableGroup_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{ID: "test", Name: "Test", Enabled: false}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableGroup(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test-group"] = &model.Group{ID: "test-group", Name: "Test Group", Enabled: true}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test-group/disable", nil)
	setPathValue(req, "id", "test-group")
	w := httptest.NewRecorder()

	h.DisableGroup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var group model.Group
	if err := json.NewDecoder(w.Body).Decode(&group); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if group.Enabled {
		t.Error("group should be disabled")
	}

	if st.groups["test-group"].Enabled {
		t.Error("group in store should be disabled")
	}
}

func TestDisableGroup_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/non-existent/disable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.DisableGroup(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDisableGroup_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups//disable", nil)
	w := httptest.NewRecorder()

	h.DisableGroup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDisableGroup_GetError(t *testing.T) {
	h, st, _ := testHandler()
	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableGroup_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.groups["test"] = &model.Group{ID: "test", Name: "Test", Enabled: true}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/groups/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableGroup(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
