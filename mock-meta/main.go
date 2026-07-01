package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

type MessageRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             *TextObject `json:"text,omitempty"`
	Template         *TemplateObject `json:"template,omitempty"`
}

type TextObject struct {
	Body string `json:"body"`
}

type TemplateObject struct {
	Name     string `json:"name"`
	Language struct {
		Code string `json:"code"`
	} `json:"language"`
	Components []any `json:"components,omitempty"`
}

type TriggerWebhookRequest struct {
	CustomerID string `json:"customer_id"`
	Text       string `json:"text"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	webhookURL := os.Getenv("APP_WEBHOOK_URL")
	if webhookURL == "" {
		webhookURL = "http://localhost:8080/webhook"
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Message templates endpoint
	http.HandleFunc("/v20.0/mock-waba-id/message_templates", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"data": [
					{
						"name": "sample_purchase_feedback",
						"category": "marketing",
						"language": "en",
						"status": "APPROVED",
						"components": [
							{
								"type": "BODY",
								"text": "Thank you for your purchase! We value your feedback."
							}
						]
					},
					{
						"name": "sample_flight_confirmation",
						"category": "utility",
						"language": "en",
						"status": "APPROVED",
						"components": [
							{
								"type": "BODY",
								"text": "Your flight has been confirmed. Details: {{1}}"
							}
						]
					}
				]
			}`))
			return
		}

		if r.Method == http.MethodPost {
			// Create template
			body, _ := io.ReadAll(r.Body)
			log.Printf("[Mock Meta] Template creation request received: %s", string(body))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id": "mock-template-id-123", "status": "PENDING"}`))

			// Simulate approval webhook in background after 2 seconds
			go func() {
				time.Sleep(2 * time.Second)
				sendTemplateApprovalWebhook(webhookURL)
			}()
			return
		}
		
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Send message endpoint
	http.HandleFunc("/v20.0/mock-phone-id/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Auth check
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mock-access-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": {"message": "Invalid OAuth access token.", "type": "OAuthException", "code": 190}}`))
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var req MessageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error": {"message": "Invalid request parameter.", "type": "OAuthException", "code": 100}}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")

		// Route based on destination number to simulate errors
		toNum := req.To
		if len(toNum) > 0 && toNum[0] == '+' {
			toNum = toNum[1:]
		}

		switch toNum {
		case "12345678901": // Trigger error 131047: Re-engagement window expired
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{
				"error": {
					"message": "(#131047) Re-engagement message failed",
					"type": "OAuthException",
					"code": 131047,
					"error_data": {
						"messaging_product": "whatsapp",
						"details": "Message failed to send because more than 24 hours have passed since the customer last replied to this number."
					}
				}
			}`))
			log.Printf("[Mock Meta] Simulated error 131047 for %s", req.To)

		case "12345678902": // Trigger error 131048: Spam rate limit hit
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{
				"error": {
					"message": "(#131048) Spam rate limit hit",
					"type": "OAuthException",
					"code": 131048,
					"error_data": {
						"messaging_product": "whatsapp",
						"details": "Message blocked due to spam rate limiting policy."
					}
				}
			}`))
			log.Printf("[Mock Meta] Simulated error 131048 for %s", req.To)

		case "12345678903": // Trigger error 131049: Frequency cap reached
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{
				"error": {
					"message": "(#131049) Frequency cap reached",
					"type": "OAuthException",
					"code": 131049,
					"error_data": {
						"messaging_product": "whatsapp",
						"details": "Customer has reached their daily frequency cap for marketing messages."
					}
				}
			}`))
			log.Printf("[Mock Meta] Simulated error 131049 for %s", req.To)

		case "12345678904": // Trigger error 470: Unknown Meta error (Transient/Retryable)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{
				"error": {
					"message": "(#470) Unknown Meta API internal error",
					"type": "OAuthException",
					"code": 470,
					"error_data": {
						"messaging_product": "whatsapp",
						"details": "A transient error occurred on the server. Please retry."
					}
				}
			}`))
			log.Printf("[Mock Meta] Simulated transient error 470 for %s", req.To)

		case "12345678905": // Trigger error 131030: Sandbox allowed list mismatch
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{
				"error": {
					"message": "(#131030) Recipient phone number not in allowed list",
					"type": "OAuthException",
					"code": 131030,
					"error_data": {
						"messaging_product": "whatsapp",
						"details": "Recipient phone number is not in the allowed list. In Sandbox mode, you can only send messages to pre-registered, verified phone numbers."
					}
				}
			}`))
			log.Printf("[Mock Meta] Simulated sandbox error 131030 for %s", req.To)

		default: // Success path
			w.WriteHeader(http.StatusOK)
			msgID := fmt.Sprintf("wamid.HBgM%d", time.Now().UnixNano())
			w.Write([]byte(fmt.Sprintf(`{
				"messaging_product": "whatsapp",
				"contacts": [
					{
						"input": "%s",
						"wa_id": "%s"
					}
				],
				"messages": [
					{
						"id": "%s"
					}
				]
			}`, req.To, req.To, msgID)))
			log.Printf("[Mock Meta] Sent successfully to %s. MsgID: %s", req.To, msgID)

			// Simulate delivery and read webhooks asynchronously
			go func(to, id string) {
				time.Sleep(1 * time.Second)
				sendWebhookStatus(webhookURL, to, id, "delivered")
				time.Sleep(1 * time.Second)
				sendWebhookStatus(webhookURL, to, id, "read")
			}(req.To, msgID)
		}
	})

	// Helper endpoint to trigger an inbound webhook (simulating a user message)
	http.HandleFunc("/trigger-webhook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var req TriggerWebhookRequest
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.CustomerID == "" {
			req.CustomerID = "+12345678900"
		}
		if req.Text == "" {
			req.Text = "Hello"
		}

		err = triggerInboundWebhook(webhookURL, req.CustomerID, req.Text)
		if err != nil {
			log.Printf("[Mock Meta] Failed to trigger inbound webhook: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf(`{"status": "error", "message": "%v"}`, err)))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "success", "message": "Inbound webhook triggered"}`))
	})

	log.Printf("[Mock Meta] Server starting on port %s...", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func triggerInboundWebhook(url, customerID, text string) error {
	timestamp := time.Now().Unix()
	msgID := fmt.Sprintf("wamid.inbound%d", time.Now().UnixNano())

	payload := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []any{
			map[string]any{
				"id": "mock-waba-id",
				"changes": []any{
					map[string]any{
						"value": map[string]any{
							"messaging_product": "whatsapp",
							"metadata": map[string]any{
								"display_phone_number": "15555555555",
								"phone_number_id":      "mock-phone-id",
							},
							"contacts": []any{
								map[string]any{
									"profile": map[string]any{
										"name": "Jane Doe",
									},
									"wa_id": customerID,
								},
							},
							"messages": []any{
								map[string]any{
									"from":      customerID,
									"id":        msgID,
									"timestamp": fmt.Sprintf("%d", timestamp),
									"text": map[string]any{
										"body": text,
									},
									"type": "text",
								},
							},
						},
						"field": "messages",
					},
				},
			},
		},
	}

	data, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func sendWebhookStatus(url, customerID, msgID, status string) {
	timestamp := time.Now().Unix()
	payload := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []any{
			map[string]any{
				"id": "mock-waba-id",
				"changes": []any{
					map[string]any{
						"value": map[string]any{
							"messaging_product": "whatsapp",
							"metadata": map[string]any{
								"display_phone_number": "15555555555",
								"phone_number_id":      "mock-phone-id",
							},
							"statuses": []any{
								map[string]any{
									"id":           msgID,
									"status":       status,
									"timestamp":    fmt.Sprintf("%d", timestamp),
									"recipient_id": customerID,
								},
							},
						},
						"field": "messages",
					},
				},
			},
		},
	}

	data, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("[Mock Meta] Error sending status webhook: %v", err)
		return
	}
	defer resp.Body.Close()
}

func sendTemplateApprovalWebhook(url string) {
	timestamp := time.Now().Unix()
	payload := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []any{
			map[string]any{
				"id": "mock-waba-id",
				"changes": []any{
					map[string]any{
						"value": map[string]any{
							"event": "TEMPLATE_STATUS_UPDATE",
							"message_template_id": "mock-template-id-123",
							"message_template_name": "sample_custom_template",
							"message_template_language": "en",
							"event_type": "APPROVED",
							"timestamp": timestamp,
						},
						"field": "message_template_status_update",
					},
				},
			},
		},
	}

	data, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("[Mock Meta] Error sending template approval webhook: %v", err)
		return
	}
	defer resp.Body.Close()
}
