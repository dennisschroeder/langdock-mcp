package main

import (
	"context"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The API accepts lowercase roles only.
var userRoles = []string{"member", "editor", "admin"}

type UserInvite struct {
	Email string `json:"email" jsonschema:"email address to invite"`
	Role  string `json:"role,omitempty" jsonschema:"workspace role, default member"`
}

type InviteUsersInput struct {
	Users []UserInvite `json:"users" jsonschema:"users to invite"`
}

type UpdateUserRoleInput struct {
	Email string `json:"email" jsonschema:"email of an active workspace member, matched case-insensitively"`
	Role  string `json:"role" jsonschema:"new workspace role"`
}

type DeactivateUserInput struct {
	Email string `json:"email" jsonschema:"email of an active workspace member"`
}

func (s *Server) registerUsers() {
	// Inviting adds access and sends emails but removes nothing, so it is not
	// destructive; repeating it may send another email, so it is not idempotent.
	invite := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(true)}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "invite_users",
		Description: "Invite people to the Langdock workspace. Side effects: each new address immediately receives an invitation email from Langdock (with a sign-in link unless SAML is enforced), and pending join requests from these addresses are approved, granting them workspace access. Existing members are skipped silently and keep their role. A successful response can still list rejected addresses in invalidEmails (e.g. a domain the workspace does not allow); check both successfulInvites and invalidEmails. Confirm the address list and roles with the user before calling.",
		Annotations: invite,
		InputSchema: schemaFor[InviteUsersInput](func(sc *jsonschema.Schema) {
			at(sc, "users").MinItems = ptr(1)
			emailSchema(sc, "users", "[]", "email")
			enum(sc, userRoles, "users", "[]", "role")
		}),
	}, s.inviteUsers)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "update_user_role",
		Description: "Change an active workspace member's role to member, editor or admin. Side effect: the member's permissions change immediately; granting admin gives full control over the workspace, including user management, and demoting an admin removes it. The API refuses to demote the last active admin. Setting the current role again is a no-op that succeeds.",
		Annotations: destructive,
		InputSchema: schemaFor[UpdateUserRoleInput](func(sc *jsonschema.Schema) {
			emailSchema(sc, "email")
			enum(sc, userRoles, "role")
		}),
	}, s.updateUserRole)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "deactivate_user",
		Description: "Deactivate a workspace member. Side effect: the user loses access to the workspace immediately. Their conversations, agents, files and integrations are kept and restored if they are added to the workspace again; the API itself offers no reactivation. Confirm the address with the user before calling.",
		Annotations: destructive,
		InputSchema: schemaFor[DeactivateUserInput](func(sc *jsonschema.Schema) {
			emailSchema(sc, "email")
		}),
	}, s.deactivateUser)
}

// jsonschema-go does not validate format, so minLength is what rejects an
// empty address locally; format still tells the model what is expected.
func emailSchema(sc *jsonschema.Schema, path ...string) {
	target := at(sc, path...)
	target.Format = "email"
	target.MinLength = ptr(3)
}

func (s *Server) inviteUsers(ctx context.Context, _ *mcp.CallToolRequest, in InviteUsersInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/user-management/v1/invite", in)
}

func (s *Server) updateUserRole(ctx context.Context, _ *mcp.CallToolRequest, in UpdateUserRoleInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/user-management/v1/update-user-role", in)
}

func (s *Server) deactivateUser(ctx context.Context, _ *mcp.CallToolRequest, in DeactivateUserInput) (*mcp.CallToolResult, any, error) {
	return s.call(ctx, http.MethodPost, "/user-management/v1/deactivate-user", in)
}

// userStatusHint covers the User Management API, whose keys must be created
// by a workspace admin.
func userStatusHint(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid request body or role, or the change would leave the workspace without an active admin"
	case http.StatusUnauthorized:
		return "invalid, missing or expired API key, or the admin who created the key no longer exists"
	case http.StatusForbidden:
		return "API key lacks the USER_MANAGEMENT_API scope"
	case http.StatusNotFound:
		return "no active human workspace member with this email"
	}
	return ""
}
