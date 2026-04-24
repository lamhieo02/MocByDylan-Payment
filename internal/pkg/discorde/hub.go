package discorde

import (
	"sync"
)

var currentHub = NewHub(nil, NewScope())

type Hub struct {
	mu    sync.RWMutex
	stack *stack
}

type layer struct {
	mu     sync.RWMutex
	client *Client
	scope  *Scope
}

func (l *layer) Client() *Client {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.client
}

func (l *layer) SetClient(c *Client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.client = c
}

type stack []*layer

func NewHub(client *Client, scope *Scope) *Hub {
	return &Hub{
		stack: &stack{
			{
				client: client,
				scope:  scope,
			},
		},
	}
}

func CurrentHub() *Hub {
	return currentHub
}

func (hub *Hub) stackTop() *layer {
	hub.mu.RLock()
	defer hub.mu.RUnlock()

	stack := hub.stack
	stackLen := len(*stack)
	top := (*stack)[stackLen-1]
	return top
}

func (hub *Hub) Scope() *Scope {
	top := hub.stackTop()
	return top.scope
}

func (hub *Hub) Client() *Client {
	top := hub.stackTop()
	return top.Client()
}

// PushScope pushes a new scope for the current Hub and reuses previously bound Client.
func (hub *Hub) PushScope() *Scope {
	top := hub.stackTop()

	var scope *Scope
	if top.scope != nil {
		scope = top.scope.Clone()
	} else {
		scope = NewScope()
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()

	*hub.stack = append(*hub.stack, &layer{
		client: top.Client(),
		scope:  scope,
	})

	return scope
}

func (hub *Hub) PopScope() {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	stack := *hub.stack
	stackLen := len(stack)
	if stackLen > 1 {
		// Never pop the last item off the stack, the stack should always have
		// at least one item.
		*hub.stack = stack[0 : stackLen-1]
	}
}

func (hub *Hub) BindClient(client *Client) {
	top := hub.stackTop()
	top.SetClient(client)
}

func (hub *Hub) WithScope(f func(scope *Scope)) {
	scope := hub.PushScope()
	defer hub.PopScope()
	f(scope)
}

func (hub *Hub) CaptureException(exception error) {
	client, scope := hub.Client(), hub.Scope()
	if client == nil || scope == nil {
		return
	}
	client.CaptureException(exception, scope)
}

func (hub *Hub) CaptureMessage(message string) {
	client, scope := hub.Client(), hub.Scope()
	if client == nil || scope == nil {
		return
	}
	client.CaptureMessage(message, scope)
}
