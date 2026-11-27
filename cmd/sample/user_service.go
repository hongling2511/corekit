package main

import (
	"context"
	"slices"
	"strconv"
	"sync"
)

// The service layer knows nothing of HTTP: it takes a context.Context and
// plain structs. Users live in memory until the sample gets a database.

// User is a sample resource.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateUser is the input to userService.Create.
type CreateUser struct {
	Name string
}

type userService struct {
	mu     sync.Mutex
	users  []User
	nextID int
}

func newUserService() *userService { return &userService{nextID: 1} }

// List returns every user in creation order.
func (s *userService) List(ctx context.Context) ([]User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.users), nil
}

// Get returns the user with id, or ErrUserNotFound.
func (s *userService) Get(ctx context.Context, id string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.users, func(u User) bool { return u.ID == id })
	if i < 0 {
		return User{}, ErrUserNotFound.Wrap(nil, "user "+id)
	}
	return s.users[i], nil
}

// Create adds a user and returns it with its new ID.
func (s *userService) Create(ctx context.Context, in CreateUser) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := User{ID: strconv.Itoa(s.nextID), Name: in.Name}
	s.nextID++
	s.users = append(s.users, u)
	return u, nil
}
