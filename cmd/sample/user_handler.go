package main

import (
	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/httpx"
)

// userHandler adapts HTTP to the user service; gin.Context stops here.
type userHandler struct {
	svc *userService
}

func (h *userHandler) routes(r gin.IRouter) {
	r.GET("/users", h.list)
	r.GET("/users/:id", h.get)
	r.POST("/users", h.create)
}

func (h *userHandler) list(c *gin.Context) {
	users, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.List(c, users, len(users))
}

func (h *userHandler) get(c *gin.Context) {
	user, err := h.svc.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, user)
}

type createUserRequest struct {
	Name string `json:"name" binding:"required"`
}

func (h *userHandler) create(c *gin.Context) {
	var req createUserRequest
	if err := httpx.Bind(c, &req); err != nil {
		httpx.Fail(c, err)
		return
	}
	user, err := h.svc.Create(c.Request.Context(), CreateUser(req))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, user)
}
