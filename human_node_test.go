package petalflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestHumanNode_Approval(t *testing.T) {
	tests := []struct {
		name     string
		approved bool
	}{
		{name: "approved", approved: true},
		{name: "rejected", approved: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &AutoApproveHandler{Approved: tt.approved}

			node := NewHumanNode("human1", HumanNodeConfig{
				RequestType: HumanRequestApproval,
				Prompt:      "Please approve this request",
				Handler:     handler,
				OutputVar:   "human_response",
			})

			env := NewEnvelope()
			env.SetVar("data", "test")

			result, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check response stored
			resp, ok := result.GetVar("human_response")
			if !ok {
				t.Fatal("human_response not set")
			}

			humanResp := resp.(*HumanResponse)
			if humanResp.Approved != tt.approved {
				t.Errorf("approved = %v, want %v", humanResp.Approved, tt.approved)
			}

			// Check approval status stored directly
			approved, ok := result.GetVar("human_response_approved")
			if !ok {
				t.Fatal("human_response_approved not set")
			}
			if approved.(bool) != tt.approved {
				t.Errorf("human_response_approved = %v, want %v", approved, tt.approved)
			}
		})
	}
}

func TestHumanNode_Choice(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		// Verify options were passed
		if len(req.Options) != 3 {
			t.Errorf("expected 3 options, got %d", len(req.Options))
		}

		return &HumanResponse{
			RequestID:   req.ID,
			Choice:      "option_b",
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestChoice,
		Prompt:      "Choose an option",
		Options: []HumanOption{
			{ID: "option_a", Label: "Option A", Description: "First option"},
			{ID: "option_b", Label: "Option B", Description: "Second option"},
			{ID: "option_c", Label: "Option C", Description: "Third option"},
		},
		Handler:   handler,
		OutputVar: "human_response",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, _ := result.GetVar("human_response")
	humanResp := resp.(*HumanResponse)
	if humanResp.Choice != "option_b" {
		t.Errorf("choice = %q, want %q", humanResp.Choice, "option_b")
	}
}

func TestHumanNode_Edit(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		// Return edited data
		return &HumanResponse{
			RequestID: req.ID,
			Data: map[string]any{
				"title":       "Edited Title",
				"description": "Edited description",
			},
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestEdit,
		Prompt:      "Edit the document",
		InputVars:   []string{"title", "description"},
		Handler:     handler,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	env.SetVar("title", "Original Title")
	env.SetVar("description", "Original description")

	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check edited data was merged
	title, _ := result.GetVar("title")
	if title != "Edited Title" {
		t.Errorf("title = %q, want %q", title, "Edited Title")
	}

	desc, _ := result.GetVar("description")
	if desc != "Edited description" {
		t.Errorf("description = %q, want %q", desc, "Edited description")
	}
}

func TestHumanNode_Input(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		return &HumanResponse{
			RequestID:   req.ID,
			Data:        "User provided input text",
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestInput,
		Prompt:      "Enter your feedback",
		Handler:     handler,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, _ := result.GetVar("human_response")
	humanResp := resp.(*HumanResponse)
	if humanResp.Data != "User provided input text" {
		t.Errorf("data = %v, want %q", humanResp.Data, "User provided input text")
	}
}

func TestHumanNode_Review(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		return &HumanResponse{
			RequestID:   req.ID,
			Approved:    true,
			Notes:       "Looks good, approved with minor suggestions",
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestReview,
		Prompt:      "Review this document",
		Handler:     handler,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, _ := result.GetVar("human_response")
	humanResp := resp.(*HumanResponse)
	if humanResp.Notes == "" {
		t.Error("expected notes in review response")
	}
}

func TestHumanNode_PromptTemplate(t *testing.T) {
	var receivedPrompt string

	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		receivedPrompt = req.Prompt
		return &HumanResponse{
			RequestID:   req.ID,
			Approved:    true,
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType:    HumanRequestApproval,
		PromptTemplate: "Please approve: {{.vars.document_name}} by {{.vars.author}}",
		Handler:        handler,
		OutputVar:      "human_response",
	})

	env := NewEnvelope()
	env.SetVar("document_name", "Report Q4")
	env.SetVar("author", "John Doe")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Please approve: Report Q4 by John Doe"
	if receivedPrompt != expected {
		t.Errorf("prompt = %q, want %q", receivedPrompt, expected)
	}
}

func TestHumanNode_InputVars(t *testing.T) {
	var receivedData any

	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		receivedData = req.Data
		return &HumanResponse{
			RequestID:   req.ID,
			Approved:    true,
			RespondedAt: time.Now(),
		}, nil
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		InputVars:   []string{"important", "relevant"},
		Handler:     handler,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	env.SetVar("important", "yes")
	env.SetVar("relevant", "also yes")
	env.SetVar("secret", "should not be included")

	_, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dataMap := receivedData.(map[string]any)
	if _, ok := dataMap["important"]; !ok {
		t.Error("important var should be included")
	}
	if _, ok := dataMap["relevant"]; !ok {
		t.Error("relevant var should be included")
	}
	if _, ok := dataMap["secret"]; ok {
		t.Error("secret var should NOT be included")
	}
}

func TestHumanNode_Timeout_Fail(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		// Simulate slow response
		select {
		case <-time.After(200 * time.Millisecond):
			return &HumanResponse{RequestID: req.ID}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		Handler:     handler,
		Timeout:     50 * time.Millisecond,
		OnTimeout:   HumanTimeoutFail,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestHumanNode_Timeout_Default(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType:   HumanRequestApproval,
		Prompt:        "Approve",
		Handler:       handler,
		Timeout:       50 * time.Millisecond,
		OnTimeout:     HumanTimeoutDefault,
		DefaultOption: "approve",
		OutputVar:     "human_response",
	})

	env := NewEnvelope()
	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, _ := result.GetVar("human_response")
	humanResp := resp.(*HumanResponse)
	if humanResp.Choice != "approve" {
		t.Errorf("expected default choice 'approve', got %q", humanResp.Choice)
	}
	if !humanResp.Approved {
		t.Error("expected approved = true for 'approve' default")
	}
	if humanResp.Meta["timeout"] != true {
		t.Error("expected timeout meta flag")
	}
}

func TestHumanNode_Timeout_Skip(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		Handler:     handler,
		Timeout:     50 * time.Millisecond,
		OnTimeout:   HumanTimeoutSkip,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	env.SetVar("original", "value")

	result, err := node.Run(context.Background(), env)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original vars should be preserved
	if val, _ := result.GetVar("original"); val != "value" {
		t.Error("original var should be preserved")
	}

	resp, _ := result.GetVar("human_response")
	humanResp := resp.(*HumanResponse)
	if humanResp.Meta["skipped"] != true {
		t.Error("expected skipped meta flag")
	}
}

func TestHumanNode_NoHandler(t *testing.T) {
	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		// No handler configured
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for missing handler")
	}
}

func TestHumanNode_HandlerError(t *testing.T) {
	expectedErr := errors.New("handler failed")

	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		return nil, expectedErr
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		Handler:     handler,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error from handler")
	}
}

func TestHumanNode_InvalidPromptTemplate(t *testing.T) {
	handler := NewAutoApproveHandler()

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType:    HumanRequestApproval,
		PromptTemplate: "{{.invalid syntax",
		Handler:        handler,
	})

	env := NewEnvelope()
	_, err := node.Run(context.Background(), env)

	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestHumanNode_EnvelopeIsolation(t *testing.T) {
	handler := NewAutoApproveHandler()

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		Handler:     handler,
		OutputVar:   "human_response",
	})

	env := NewEnvelope()
	env.SetVar("original", "value")

	result, _ := node.Run(context.Background(), env)

	// Modify result
	result.SetVar("modified", "new")

	// Original should not be modified
	if _, ok := env.GetVar("modified"); ok {
		t.Error("original envelope should not be modified")
	}
	if _, ok := env.GetVar("human_response"); ok {
		t.Error("original envelope should not have human_response")
	}
}

func TestHumanNode_ContextCancellation(t *testing.T) {
	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	node := NewHumanNode("human1", HumanNodeConfig{
		RequestType: HumanRequestApproval,
		Prompt:      "Approve",
		Handler:     handler,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	env := NewEnvelope()
	_, err := node.Run(ctx, env)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestHumanNode_ID(t *testing.T) {
	node := NewHumanNode("my_human", HumanNodeConfig{})

	if node.ID() != "my_human" {
		t.Errorf("ID = %q, want %q", node.ID(), "my_human")
	}
}

func TestHumanNode_Kind(t *testing.T) {
	node := NewHumanNode("human1", HumanNodeConfig{})

	if node.Kind() != NodeKindHuman {
		t.Errorf("Kind = %q, want %q", node.Kind(), NodeKindHuman)
	}
}

// ChannelHumanHandler tests

func TestChannelHumanHandler_RequestResponse(t *testing.T) {
	handler := NewChannelHumanHandler(10)

	// Start goroutine to handle request
	go func() {
		req := <-handler.Requests()
		handler.Respond(&HumanResponse{
			RequestID:   req.ID,
			Approved:    true,
			RespondedAt: time.Now(),
		})
	}()

	resp, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Approved {
		t.Error("expected approved = true")
	}
}

func TestChannelHumanHandler_ContextCancellation(t *testing.T) {
	handler := NewChannelHumanHandler(10)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := handler.Request(ctx, &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestChannelHumanHandler_PendingCount(t *testing.T) {
	handler := NewChannelHumanHandler(10)

	if handler.PendingCount() != 0 {
		t.Errorf("expected 0 pending, got %d", handler.PendingCount())
	}

	// Start request in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Request(context.Background(), &HumanRequest{
			ID:     "req-1",
			Type:   HumanRequestApproval,
			Prompt: "Test",
		})
	}()

	// Wait for request to be received
	<-handler.Requests()

	if handler.PendingCount() != 1 {
		t.Errorf("expected 1 pending, got %d", handler.PendingCount())
	}

	// Respond and wait for completion
	handler.Respond(&HumanResponse{
		RequestID:   "req-1",
		RespondedAt: time.Now(),
	})
	wg.Wait()

	if handler.PendingCount() != 0 {
		t.Errorf("expected 0 pending after response, got %d", handler.PendingCount())
	}
}

func TestChannelHumanHandler_RespondUnknown(t *testing.T) {
	handler := NewChannelHumanHandler(10)

	err := handler.Respond(&HumanResponse{
		RequestID:   "unknown-id",
		RespondedAt: time.Now(),
	})

	if err == nil {
		t.Error("expected error for unknown request ID")
	}
}

// CallbackHumanHandler tests

func TestCallbackHumanHandler_Request(t *testing.T) {
	called := false

	handler := NewCallbackHumanHandler(func(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
		called = true
		return &HumanResponse{
			RequestID:   req.ID,
			Approved:    true,
			RespondedAt: time.Now(),
		}, nil
	})

	resp, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("callback was not called")
	}
	if !resp.Approved {
		t.Error("expected approved = true")
	}
}

func TestCallbackHumanHandler_NilCallback(t *testing.T) {
	handler := &CallbackHumanHandler{callback: nil}

	_, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err == nil {
		t.Error("expected error for nil callback")
	}
}

// AutoApproveHandler tests

func TestAutoApproveHandler_Approve(t *testing.T) {
	handler := NewAutoApproveHandler()

	resp, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Approved {
		t.Error("expected approved = true")
	}
	if resp.Choice != "approve" {
		t.Errorf("expected choice 'approve', got %q", resp.Choice)
	}
}

func TestAutoApproveHandler_Reject(t *testing.T) {
	handler := NewAutoRejectHandler()

	resp, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Approved {
		t.Error("expected approved = false")
	}
	if resp.Choice != "reject" {
		t.Errorf("expected choice 'reject', got %q", resp.Choice)
	}
}

func TestAutoApproveHandler_WithDelay(t *testing.T) {
	handler := &AutoApproveHandler{
		Approved: true,
		Delay:    50 * time.Millisecond,
	}

	start := time.Now()
	_, err := handler.Request(context.Background(), &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected delay of at least 50ms, got %v", elapsed)
	}
}

func TestAutoApproveHandler_ContextCancellation(t *testing.T) {
	handler := &AutoApproveHandler{
		Approved: true,
		Delay:    1 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := handler.Request(ctx, &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// QueuedHumanHandler tests

func TestQueuedHumanHandler_RequestResponse(t *testing.T) {
	handler := NewQueuedHumanHandler()

	// Start request in goroutine
	var resp *HumanResponse
	var respErr error
	done := make(chan struct{})

	go func() {
		resp, respErr = handler.Request(context.Background(), &HumanRequest{
			ID:     "req-1",
			Type:   HumanRequestApproval,
			Prompt: "Test",
		})
		close(done)
	}()

	// Wait for request to be queued
	time.Sleep(20 * time.Millisecond)

	// Check pending
	pending := handler.ListPending()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// Get request
	req, ok := handler.GetRequest("req-1")
	if !ok {
		t.Fatal("request not found")
	}
	if req.Prompt != "Test" {
		t.Errorf("wrong prompt: %s", req.Prompt)
	}

	// Respond
	err := handler.Respond("req-1", &HumanResponse{
		Approved: true,
	})
	if err != nil {
		t.Fatalf("respond error: %v", err)
	}

	// Wait for completion
	<-done

	if respErr != nil {
		t.Fatalf("request error: %v", respErr)
	}
	if !resp.Approved {
		t.Error("expected approved = true")
	}
}

func TestQueuedHumanHandler_RespondUnknown(t *testing.T) {
	handler := NewQueuedHumanHandler()

	err := handler.Respond("unknown-id", &HumanResponse{
		Approved: true,
	})

	if err == nil {
		t.Error("expected error for unknown request ID")
	}
}

func TestQueuedHumanHandler_ContextCancellation(t *testing.T) {
	handler := NewQueuedHumanHandler()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := handler.Request(ctx, &HumanRequest{
		ID:     "req-1",
		Type:   HumanRequestApproval,
		Prompt: "Test",
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestQueuedHumanHandler_GetRequestUnknown(t *testing.T) {
	handler := NewQueuedHumanHandler()

	_, ok := handler.GetRequest("unknown-id")
	if ok {
		t.Error("expected not found for unknown ID")
	}
}
