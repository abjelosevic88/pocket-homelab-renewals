// Package pocket talks to sibling Pocket apps: the ones Renewals finds dates
// in (Documents, Home, Garage, Inventory, Monitoring, Money, Email, Travel)
// and the ones it writes to (Money, Calendar, Tasks, Contacts). Same
// bearer-token JSON API everywhere.
package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

// ErrNotFound is a 404 from the sibling app.
var ErrNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		_ = json.Unmarshal(b, &e)
		if e.Error != "" {
			return fmt.Errorf("HTTP %d: %s", res.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app: /healthz plus a token check.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// Do is a raw JSON call (for a connection test).
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, v any) error {
	return c.do(ctx, method, path, q, body, v)
}

// list decodes a list that may be bare or wrapped in {key:[…]} / {results:[…]}.
func list[T any](raw json.RawMessage, keys ...string) ([]T, error) {
	var out []T
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, nil
	}
	var wrap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	for _, k := range append(keys, "results", "items") {
		if b, ok := wrap[k]; ok {
			if err := json.Unmarshal(b, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return []T{}, nil
}

// ---------------- Pocket Money ----------------

// Account is a Money account the person can see.
type Account struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Everyday bool   `json:"everyday"`
	Archived bool   `json:"archived"`
	Role     string `json:"role"` // owner | editor | viewer
}

// Accounts lists the accounts the person can book on.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var all []Account
	if err := c.do(ctx, http.MethodGet, "/api/v1/accounts", nil, nil, &all); err != nil {
		return nil, err
	}
	out := []Account{}
	for _, a := range all {
		if !a.Archived && a.Role != "viewer" {
			out = append(out, a)
		}
	}
	return out, nil
}

// Category is a Money category.
type Category struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Kind     string `json:"kind"`
	Archived bool   `json:"archived"`
}

// Categories lists the expense categories usable on an account.
func (c *Client) Categories(ctx context.Context, accountID int64) ([]Category, error) {
	var all []Category
	path := "/api/v1/categories"
	if accountID > 0 {
		path = fmt.Sprintf("/api/v1/accounts/%d/categories", accountID)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &all); err != nil {
		return nil, err
	}
	out := []Category{}
	for _, ct := range all {
		if !ct.Archived && ct.Kind != "income" {
			out = append(out, ct)
		}
	}
	return out, nil
}

// Tx is what Renewals books: always an expense, in the cost's own currency
// (Money converts to the account's).
type Tx struct {
	AccountID   int64
	Date        string
	Amount      int64 // minor units of Currency
	Currency    string
	Description string
	Payee       string
	CategoryID  int64
	Tags        []string
	Notes       string
	ExternalID  string
}

// CreateTx books a new expense and returns its id.
func (c *Client) CreateTx(ctx context.Context, t Tx) (int64, error) {
	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}
	body := map[string]any{"kind": "expense", "account_id": t.AccountID, "date": t.Date, "amount": t.Amount, "currency": t.Currency,
		"description": t.Description, "payee": t.Payee, "tags": tags, "notes": t.Notes, "source": "renewals", "external_id": t.ExternalID}
	if t.CategoryID > 0 {
		body["category_id"] = t.CategoryID
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/transactions", nil, body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// UpdateTx changes what Renewals owns on the transaction: date, amount,
// description and payee. Account, category and tags stay as the person left them.
func (c *Client) UpdateTx(ctx context.Context, id int64, t Tx) error {
	body := map[string]any{"date": t.Date, "amount": t.Amount, "currency": t.Currency, "description": t.Description, "payee": t.Payee}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/api/v1/transactions/%d", id), nil, body, nil)
}

// DeleteTx removes a transaction; one already gone counts as deleted.
func (c *Client) DeleteTx(ctx context.Context, id int64) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/transactions/%d", id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---------------- Pocket Calendar ----------------

// Calendar is one of the person's calendars.
type Calendar struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Writable bool   `json:"writable"`
	Default  bool   `json:"is_default"`
}

// Calendars lists the calendars the person can write to.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/calendars", nil, nil, &raw); err != nil {
		return nil, err
	}
	all, err := list[Calendar](raw, "calendars")
	if err != nil {
		return nil, err
	}
	out := []Calendar{}
	for _, cal := range all {
		if cal.Writable {
			out = append(out, cal)
		}
	}
	return out, nil
}

// Event is an all-day event Renewals keeps in the calendar for a renewal.
type Event struct {
	CalendarID  int64
	Title       string
	Description string
	URL         string
	Date        string
}

func (e Event) body() map[string]any {
	b := map[string]any{"title": e.Title, "description": e.Description, "url": e.URL, "all_day": true, "start": e.Date, "busy": false, "categories": []string{"Renewals"}}
	if e.CalendarID > 0 {
		b["calendar_id"] = e.CalendarID
	}
	return b
}

// CreateEvent adds the event and returns its id.
func (c *Client) CreateEvent(ctx context.Context, e Event) (int64, error) {
	var out struct {
		EventID int64 `json:"event_id"`
		ID      any   `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/events", nil, e.body(), &out); err != nil {
		return 0, err
	}
	if out.EventID > 0 {
		return out.EventID, nil
	}
	// a bare {"id": 12} or an occurrence id like "12:20261104"
	var id int64
	_, _ = fmt.Sscan(strings.SplitN(fmt.Sprint(out.ID), ":", 2)[0], &id)
	return id, nil
}

// UpdateEvent moves or renames the event.
func (c *Client) UpdateEvent(ctx context.Context, id int64, e Event) error {
	b := e.body()
	delete(b, "calendar_id")
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/api/v1/events/%d", id), nil, b, nil)
}

// DeleteEvent removes the event; one already gone counts as deleted.
func (c *Client) DeleteEvent(ctx context.Context, id int64) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/events/%d", id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---------------- Pocket Tasks ----------------

// Task is a task in Pocket Tasks.
type Task struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// CreateTask adds a task with a link back to the renewal.
func (c *Client) CreateTask(ctx context.Context, title, notes, due, link string) (*Task, error) {
	body := map[string]any{"title": title, "notes": notes, "url": link}
	if due != "" {
		body["due"] = due
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/tasks", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------- Pocket Contacts ----------------

// Contact is a card in Pocket Contacts.
type Contact struct {
	ID     int64    `json:"id"`
	FN     string   `json:"fn"`
	Org    string   `json:"org"`
	Title  string   `json:"title"`
	Emails []string `json:"emails"`
	Phones []string `json:"phones"`
	URLs   []string `json:"urls"`
	Note   string   `json:"note"`
}

// SearchContacts finds the person's contacts.
func (c *Client) SearchContacts(ctx context.Context, q string, limit int) ([]Contact, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/search", url.Values{"q": {q}, "limit": {fmt.Sprint(limit)}}, nil, &raw); err != nil {
		return nil, err
	}
	out, err := list[Contact](raw, "contacts")
	if len(out) > limit {
		out = out[:limit]
	}
	return out, err
}

// GetContact reads one contact.
func (c *Client) GetContact(ctx context.Context, id int64) (*Contact, error) {
	var out Contact
	return &out, c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/contacts/%d", id), nil, nil, &out)
}

// text reads a text/plain answer (at most max bytes).
func (c *Client) text(ctx context.Context, path string, max int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max))
	return string(b), err
}

// ---------------- Pocket Documents ----------------

// FieldValue is a custom field's value on a document (always a string).
type FieldValue struct {
	Field int64  `json:"field"`
	Value string `json:"value"`
}

// Document is a document in Pocket Documents.
type Document struct {
	ID            int64        `json:"id"`
	Title         string       `json:"title"`
	OriginalName  string       `json:"original_name"`
	Created       string       `json:"created"`
	AddedAt       string       `json:"added_at"`
	ModifiedAt    string       `json:"modified_at"`
	Correspondent string       `json:"correspondent_name"`
	DocumentType  string       `json:"document_type_name"`
	Status        string       `json:"status"`
	CustomFields  []FieldValue `json:"custom_fields"`
}

// CustomField is a field defined in Pocket Documents.
type CustomField struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"data_type"`
}

// SearchDocuments finds the person's documents (newest first without a query).
func (c *Client) SearchDocuments(ctx context.Context, q string, limit int) ([]Document, error) {
	v := url.Values{"limit": {fmt.Sprint(limit)}, "no_facets": {"1"}}
	if q != "" {
		v.Set("q", q)
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/documents", v, nil, &raw); err != nil {
		return nil, err
	}
	return list[Document](raw, "documents")
}

// ListDocuments pages through the person's documents, last changed first.
func (c *Client) ListDocuments(ctx context.Context, offset, limit int) ([]Document, error) {
	v := url.Values{"limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(offset)}, "no_facets": {"1"}, "sort": {"-modified"}}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/documents", v, nil, &raw); err != nil {
		return nil, err
	}
	return list[Document](raw, "documents")
}

// GetDocument reads one document with its custom fields.
func (c *Client) GetDocument(ctx context.Context, id int64) (*Document, error) {
	var out struct {
		Document Document `json:"document"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/documents/%d", id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Document, nil
}

// DocumentText is a document's OCR text (the first 64 KiB).
func (c *Client) DocumentText(ctx context.Context, id int64) (string, error) {
	return c.text(ctx, fmt.Sprintf("/api/v1/documents/%d/text", id), 64<<10)
}

// CustomFields lists the fields defined in Pocket Documents.
func (c *Client) CustomFields(ctx context.Context) ([]CustomField, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/custom_fields", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[CustomField](raw, "custom_fields")
}

// ---------------- Pocket Home ----------------

// Thing is an appliance or part of a house in Pocket Home.
type Thing struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	HomeName      string `json:"home_name"`
	Brand         string `json:"brand"`
	Model         string `json:"model"`
	Serial        string `json:"serial"`
	Vendor        string `json:"vendor"`
	PurchasedOn   string `json:"purchased_on"`
	WarrantyUntil string `json:"warranty_until"`
	Price         *int64 `json:"price"`
	Currency      string `json:"currency"`
	Archived      bool   `json:"archived"`
}

// Things lists the person's appliances and parts.
func (c *Client) Things(ctx context.Context) ([]Thing, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/things", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Thing](raw, "things")
}

// ---------------- Pocket Garage ----------------

// Vehicle is a vehicle in Pocket Garage.
type Vehicle struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Make   string `json:"make"`
	Model  string `json:"model"`
	Plate  string `json:"plate"`
	Status string `json:"status"`
}

// Reminder is a dated reminder on a vehicle (registration, inspection…).
type Reminder struct {
	ID           int64  `json:"id"`
	VehicleID    int64  `json:"vehicle_id"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`
	Category     string `json:"category"`
	DueDate      string `json:"due_date"`
	IntervalDays int    `json:"interval_days"`
	Status       string `json:"status"`
}

// VehicleDocument is a paper kept with a vehicle; some run out.
type VehicleDocument struct {
	ID        int64  `json:"id"`
	VehicleID int64  `json:"vehicle_id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ExpiresOn string `json:"expires_on"`
}

// Vehicles lists the person's vehicles.
func (c *Client) Vehicles(ctx context.Context) ([]Vehicle, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/vehicles", url.Values{"bare": {"1"}}, nil, &raw); err != nil {
		return nil, err
	}
	return list[Vehicle](raw, "vehicles")
}

// VehicleReminders lists one vehicle's reminders.
func (c *Client) VehicleReminders(ctx context.Context, id int64) ([]Reminder, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/vehicles/%d/reminders", id), nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Reminder](raw, "reminders")
}

// VehicleDocuments lists one vehicle's papers.
func (c *Client) VehicleDocuments(ctx context.Context, id int64) ([]VehicleDocument, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/vehicles/%d/documents", id), nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[VehicleDocument](raw, "documents")
}

// ---------------- Pocket Inventory ----------------

// Item is a Pocket Inventory item.
type Item struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Quantity   float64  `json:"quantity"`
	Unit       string   `json:"unit"`
	Location   string   `json:"location_name"`
	Category   string   `json:"category_name"`
	Brand      string   `json:"brand"`
	Model      string   `json:"model"`
	Serial     string   `json:"serial"`
	ExpiryDate string   `json:"expiry_date"`
	Price      *float64 `json:"price"`
	Archived   bool     `json:"archived"`
}

// SearchItems looks up inventory items by name.
func (c *Client) SearchItems(ctx context.Context, q string, limit int) ([]Item, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/items", url.Values{"q": {q}, "limit": {fmt.Sprint(limit)}}, nil, &raw); err != nil {
		return nil, err
	}
	return list[Item](raw, "items")
}

// ExpiringItems lists the items with an expiry date up to days from now (expired ones too).
func (c *Client) ExpiringItems(ctx context.Context, days int) ([]Item, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/items", url.Values{"expiring": {fmt.Sprint(days)}, "sort": {"expiry"}, "limit": {"1000"}}, nil, &raw); err != nil {
		return nil, err
	}
	return list[Item](raw, "items")
}

// ---------------- Pocket Wealth ----------------

// Maturing is a deposit that matures, a fixed rate that ends or money due back.
type Maturing struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	KindLabel   string `json:"kind_label"`
	Debt        bool   `json:"debt"`
	Institution string `json:"institution"`
	Number      string `json:"number"`
	MaturesOn   string `json:"matures_on"`
	URL         string `json:"url"`
}

// Maturing lists the person's assets and debts with a date they mature or end.
func (c *Client) Maturing(ctx context.Context) ([]Maturing, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/maturing", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Maturing](raw, "items")
}

// ---------------- Pocket Pets ----------------

// Pet is one of the person's pets in Pocket Pets.
type Pet struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Species      string `json:"species"`
	Breed        string `json:"breed"`
	Microchip    string `json:"microchip"`
	PassportNo   string `json:"passport_no"`
	Insurer      string `json:"insurer"`
	PolicyNo     string `json:"policy_no"`
	InsuredUntil string `json:"insured_until"`
	PassedOn     string `json:"passed_on"`
	Archived     bool   `json:"archived"`
	Currency     string `json:"currency"`
}

// PetRecord is an entry in a pet's health log (a vet visit, a vaccine…).
type PetRecord struct {
	ID         int64  `json:"id"`
	PetID      int64  `json:"pet_id"`
	PetName    string `json:"pet_name"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Date       string `json:"date"`
	ValidUntil string `json:"valid_until"`
	Cost       int64  `json:"cost"`
	Currency   string `json:"currency"`
	ProName    string `json:"pro_name"`
}

// Pets lists the person's pets.
func (c *Client) Pets(ctx context.Context) ([]Pet, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/pets", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Pet](raw, "pets")
}

// Vaccines lists the person's vaccine records, newest first (up to 500).
func (c *Client) Vaccines(ctx context.Context) ([]PetRecord, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/records", url.Values{"kind": {"vaccine"}, "limit": {"500"}}, nil, &raw); err != nil {
		return nil, err
	}
	return list[PetRecord](raw, "records")
}

// ---------------- Pocket Wallet ----------------

// Card is one thing in Pocket Wallet: a loyalty, membership or gift card, a
// ticket, a boarding pass, an ID card, a passport, a licence.
type Card struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Group     string `json:"group"` // cards | passes | ids
	Name      string `json:"name"`
	Issuer    string `json:"issuer"`
	Number    string `json:"number"`
	Holder    string `json:"holder"`
	ValidFrom string `json:"valid_from"`
	ExpiresOn string `json:"expires_on"`
	Archived  bool   `json:"archived"`
	Balance   *int64 `json:"balance"` // what is left on a gift card, in cents
	Currency  string `json:"currency"`
}

// Cards lists the person's cards that are not archived.
func (c *Client) Cards(ctx context.Context) ([]Card, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/cards", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Card](raw, "cards")
}

// ---------------- Pocket Monitoring ----------------

// Server is a machine in Pocket Monitoring.
type Server struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Check is an uptime check; https checks know when their certificate runs out.
type Check struct {
	ID          int64  `json:"id"`
	ServerID    int64  `json:"server_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	IgnoreTLS   bool   `json:"ignore_tls"`
	CertExpires int64  `json:"cert_expires"` // unix seconds, 0 = unknown
}

// Servers lists the monitored machines.
func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/servers", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Server](raw, "servers")
}

// ServerChecks lists one machine's uptime checks.
func (c *Client) ServerChecks(ctx context.Context, id int64) ([]Check, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/servers/%d/checks", id), nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Check](raw, "checks")
}

// ---------------- Pocket Money: subscriptions ----------------

// Subscription is a recurring payment in Pocket Money.
type Subscription struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Payee    string `json:"payee"`
	URL      string `json:"url"`
	Freq     string `json:"freq"`
	Every    int    `json:"every"`
	NextDate string `json:"next_date"`
	EndDate  string `json:"end_date"`
	Status   string `json:"status"`
	Active   bool   `json:"active"`
}

// Subscriptions lists the person's recurring payments.
func (c *Client) Subscriptions(ctx context.Context) ([]Subscription, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/subscriptions", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Subscription](raw, "items", "subscriptions")
}

// ---------------- Pocket Email ----------------

// Address is a sender or recipient.
type Address struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
}

// Message is an email in Pocket Email (without its body).
type Message struct {
	ID      int64   `json:"id"`
	Subject string  `json:"subject"`
	From    Address `json:"from"`
	Date    string  `json:"date"`
	Snippet string  `json:"snippet"`
}

// SearchMessages finds email, newest first (Gmail-style operators work in q).
func (c *Client) SearchMessages(ctx context.Context, q string, limit int) ([]Message, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/messages", url.Values{"q": {q}, "limit": {fmt.Sprint(limit)}}, nil, &raw); err != nil {
		return nil, err
	}
	return list[Message](raw, "messages")
}

// MessageText reads one message's plain text.
func (c *Client) MessageText(ctx context.Context, id int64) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/messages/%d", id), nil, nil, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}

// ---------------- Pocket Travel ----------------

// Trip is a trip in Pocket Travel.
type Trip struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Status      string `json:"status"`
	Archived    bool   `json:"archived"`
}

// Trips lists the person's trips.
func (c *Client) Trips(ctx context.Context) ([]Trip, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/trips", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Trip](raw, "trips")
}
