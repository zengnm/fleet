package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"fleetd/pkg/spec"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLite(ctx context.Context, dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	store := &SQLiteStore{db: db}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`create table if not exists tools (
			id text primary key,
			module text not null,
			tool text not null,
			title text not null,
			description text not null,
			search_text text not null,
			payload text not null,
			revision integer not null,
			updated_at text not null
		);`,
		`create index if not exists idx_tools_module on tools(module);`,
		`create table if not exists secrets (
			name text primary key,
			payload text not null,
			updated_at text not null
		);`,
		`create table if not exists mcp_servers (
			id text primary key,
			payload text not null,
			updated_at text not null
		);`,
		`create table if not exists invocations (
			id text primary key,
			payload text not null,
			updated_at text not null
		);`,
		`create table if not exists fleet_pending_claims (
			pairing_id text primary key,
			device_id text not null,
			status text not null,
			payload text not null,
			updated_at text not null
		);`,
		`create index if not exists idx_fleet_pending_claims_device_id on fleet_pending_claims(device_id);`,
		`create table if not exists fleet_owned_devices (
			user_id text not null,
			device_id text not null,
			updated_at text not null,
			payload text not null,
			primary key (user_id, device_id)
		);`,
		`create index if not exists idx_fleet_owned_devices_device_id on fleet_owned_devices(device_id);`,
		`create table if not exists fleet_owned_nodes (
			user_id text not null,
			node_id text not null,
			device_id text not null,
			updated_at text not null,
			payload text not null,
			primary key (user_id, node_id)
		);`,
		`create index if not exists idx_fleet_owned_nodes_device_id on fleet_owned_nodes(device_id);`,
		`create index if not exists idx_fleet_owned_nodes_node_id on fleet_owned_nodes(node_id);`,
		`create table if not exists fleet_node_auth_states (
			device_id text primary key,
			updated_at text not null,
			payload text not null
		);`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertTool(ctx context.Context, tool spec.ToolSpec) error {
	payload, err := json.Marshal(tool)
	if err != nil {
		return err
	}
	searchText := strings.ToLower(strings.Join([]string{
		tool.ID,
		tool.Title,
		tool.Summary,
		tool.Description,
	}, " "))
	_, err = s.db.ExecContext(ctx, `
		insert into tools (id, module, tool, title, description, search_text, payload, revision, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			module=excluded.module,
			tool=excluded.tool,
			title=excluded.title,
			description=excluded.description,
			search_text=excluded.search_text,
			payload=excluded.payload,
			revision=excluded.revision,
			updated_at=excluded.updated_at
	`, tool.ID, tool.Module, tool.Tool, tool.Title, tool.Description, searchText, string(payload), tool.Revision, tool.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) DeleteTool(ctx context.Context, module, tool string) error {
	_, _, id := NormalizeTool(module, tool)
	result, err := s.db.ExecContext(ctx, `delete from tools where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) GetTool(ctx context.Context, module, tool string) (*spec.ToolSpec, error) {
	_, _, id := NormalizeTool(module, tool)
	row := s.db.QueryRowContext(ctx, `select payload from tools where id = ?`, id)
	return decodeTool(row)
}

func (s *SQLiteStore) ListTools(ctx context.Context) ([]spec.ToolSpec, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from tools order by module, tool`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTools(rows)
}

func (s *SQLiteStore) SearchTools(ctx context.Context, query string, limit int) ([]spec.ToolSpec, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		select payload from tools
		where search_text like ?
		order by module, tool
		limit ?
	`, "%"+strings.ToLower(query)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTools(rows)
}

func (s *SQLiteStore) UpsertSecret(ctx context.Context, secret spec.SecretSpec) error {
	payload, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into secrets (name, payload, updated_at)
		values (?, ?, ?)
		on conflict(name) do update set
			payload=excluded.payload,
			updated_at=excluded.updated_at
	`, secret.Name, string(payload), secret.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetSecret(ctx context.Context, name string) (*spec.SecretSpec, error) {
	row := s.db.QueryRowContext(ctx, `select payload from secrets where name = ?`, name)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var secret spec.SecretSpec
	if err := json.Unmarshal([]byte(payload), &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

func (s *SQLiteStore) UpsertMCPServer(ctx context.Context, server spec.MCPServerSpec) error {
	payload, err := json.Marshal(server)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into mcp_servers (id, payload, updated_at)
		values (?, ?, ?)
		on conflict(id) do update set
			payload=excluded.payload,
			updated_at=excluded.updated_at
	`, server.ID, string(payload), server.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetMCPServer(ctx context.Context, id string) (*spec.MCPServerSpec, error) {
	row := s.db.QueryRowContext(ctx, `select payload from mcp_servers where id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var server spec.MCPServerSpec
	if err := json.Unmarshal([]byte(payload), &server); err != nil {
		return nil, err
	}
	return &server, nil
}

func (s *SQLiteStore) ListMCPServers(ctx context.Context) ([]spec.MCPServerSpec, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from mcp_servers order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	servers := []spec.MCPServerSpec{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var server spec.MCPServerSpec
		if err := json.Unmarshal([]byte(payload), &server); err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

func (s *SQLiteStore) DeleteMCPServer(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `delete from mcp_servers where id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) SaveInvocation(ctx context.Context, session spec.InvocationSession) error {
	payload, err := json.Marshal(session)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into invocations (id, payload, updated_at)
		values (?, ?, ?)
		on conflict(id) do update set
			payload=excluded.payload,
			updated_at=excluded.updated_at
	`, session.ID, string(payload), session.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetInvocation(ctx context.Context, id string) (*spec.InvocationSession, error) {
	row := s.db.QueryRowContext(ctx, `select payload from invocations where id = ?`, id)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var session spec.InvocationSession
	if err := json.Unmarshal([]byte(payload), &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *SQLiteStore) UpsertFleetPendingClaim(ctx context.Context, claim spec.FleetPendingClaim) error {
	payload, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into fleet_pending_claims (pairing_id, device_id, status, payload, updated_at)
		values (?, ?, ?, ?, ?)
		on conflict(pairing_id) do update set
			device_id=excluded.device_id,
			status=excluded.status,
			payload=excluded.payload,
			updated_at=excluded.updated_at
	`, claim.PairingID, claim.DeviceID, claim.Status, string(payload), claim.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetFleetPendingClaim(ctx context.Context, pairingID string) (*spec.FleetPendingClaim, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_pending_claims where pairing_id = ?`, pairingID)
	return decodeFleetPendingClaim(row)
}

func (s *SQLiteStore) ListFleetPendingClaims(ctx context.Context) ([]spec.FleetPendingClaim, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from fleet_pending_claims order by updated_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFleetPendingClaims(rows)
}

func (s *SQLiteStore) DeleteFleetPendingClaim(ctx context.Context, pairingID string) error {
	result, err := s.db.ExecContext(ctx, `delete from fleet_pending_claims where pairing_id = ?`, pairingID)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (s *SQLiteStore) UpsertFleetOwnedDevice(ctx context.Context, device spec.FleetOwnedDevice) error {
	payload, err := json.Marshal(device)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into fleet_owned_devices (user_id, device_id, updated_at, payload)
		values (?, ?, ?, ?)
		on conflict(user_id, device_id) do update set
			updated_at=excluded.updated_at,
			payload=excluded.payload
	`, device.UserID, device.DeviceID, device.UpdatedAt.Format(time.RFC3339Nano), string(payload))
	return err
}

func (s *SQLiteStore) GetFleetOwnedDevice(ctx context.Context, userID, deviceID string) (*spec.FleetOwnedDevice, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_owned_devices where user_id = ? and device_id = ?`, userID, deviceID)
	return decodeFleetOwnedDevice(row)
}

func (s *SQLiteStore) FindFleetOwnedDeviceByDeviceID(ctx context.Context, deviceID string) (*spec.FleetOwnedDevice, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_owned_devices where device_id = ? order by updated_at desc limit 1`, deviceID)
	return decodeFleetOwnedDevice(row)
}

func (s *SQLiteStore) ListFleetOwnedDevices(ctx context.Context, userID string) ([]spec.FleetOwnedDevice, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from fleet_owned_devices where user_id = ? order by updated_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFleetOwnedDevices(rows)
}

func (s *SQLiteStore) DeleteFleetOwnedDevice(ctx context.Context, userID, deviceID string) error {
	result, err := s.db.ExecContext(ctx, `delete from fleet_owned_devices where user_id = ? and device_id = ?`, userID, deviceID)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (s *SQLiteStore) UpsertFleetOwnedNode(ctx context.Context, node spec.FleetOwnedNode) error {
	payload, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into fleet_owned_nodes (user_id, node_id, device_id, updated_at, payload)
		values (?, ?, ?, ?, ?)
		on conflict(user_id, node_id) do update set
			device_id=excluded.device_id,
			updated_at=excluded.updated_at,
			payload=excluded.payload
	`, node.UserID, node.NodeID, node.DeviceID, node.UpdatedAt.Format(time.RFC3339Nano), string(payload))
	return err
}

func (s *SQLiteStore) GetFleetOwnedNode(ctx context.Context, userID, nodeID string) (*spec.FleetOwnedNode, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_owned_nodes where user_id = ? and node_id = ?`, userID, nodeID)
	return decodeFleetOwnedNode(row)
}

func (s *SQLiteStore) FindFleetOwnedNodeByNodeID(ctx context.Context, nodeID string) (*spec.FleetOwnedNode, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_owned_nodes where node_id = ? order by updated_at desc limit 1`, nodeID)
	return decodeFleetOwnedNode(row)
}

func (s *SQLiteStore) ListFleetOwnedNodes(ctx context.Context, userID string) ([]spec.FleetOwnedNode, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from fleet_owned_nodes where user_id = ? order by updated_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFleetOwnedNodes(rows)
}

func (s *SQLiteStore) ListFleetOwnedNodesByDevice(ctx context.Context, userID, deviceID string) ([]spec.FleetOwnedNode, error) {
	rows, err := s.db.QueryContext(ctx, `select payload from fleet_owned_nodes where user_id = ? and device_id = ? order by updated_at desc`, userID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFleetOwnedNodes(rows)
}

func (s *SQLiteStore) DeleteFleetOwnedNode(ctx context.Context, userID, nodeID string) error {
	result, err := s.db.ExecContext(ctx, `delete from fleet_owned_nodes where user_id = ? and node_id = ?`, userID, nodeID)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (s *SQLiteStore) DeleteFleetOwnedNodesByDevice(ctx context.Context, userID, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `delete from fleet_owned_nodes where user_id = ? and device_id = ?`, userID, deviceID)
	return err
}

func (s *SQLiteStore) UpsertFleetNodeAuthState(ctx context.Context, state spec.FleetNodeAuthState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into fleet_node_auth_states (device_id, updated_at, payload)
		values (?, ?, ?)
		on conflict(device_id) do update set
			updated_at=excluded.updated_at,
			payload=excluded.payload
	`, state.DeviceID, state.UpdatedAt.Format(time.RFC3339Nano), string(payload))
	return err
}

func (s *SQLiteStore) GetFleetNodeAuthState(ctx context.Context, deviceID string) (*spec.FleetNodeAuthState, error) {
	row := s.db.QueryRowContext(ctx, `select payload from fleet_node_auth_states where device_id = ?`, deviceID)
	return decodeFleetNodeAuthState(row)
}

func (s *SQLiteStore) DeleteFleetNodeAuthState(ctx context.Context, deviceID string) error {
	result, err := s.db.ExecContext(ctx, `delete from fleet_node_auth_states where device_id = ?`, deviceID)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func scanTools(rows *sql.Rows) ([]spec.ToolSpec, error) {
	var tools []spec.ToolSpec
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var tool spec.ToolSpec
		if err := json.Unmarshal([]byte(payload), &tool); err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, rows.Err()
}

func decodeTool(row *sql.Row) (*spec.ToolSpec, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var tool spec.ToolSpec
	if err := json.Unmarshal([]byte(payload), &tool); err != nil {
		return nil, fmt.Errorf("decode tool: %w", err)
	}
	return &tool, nil
}

func decodeFleetPendingClaim(row *sql.Row) (*spec.FleetPendingClaim, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var claim spec.FleetPendingClaim
	if err := json.Unmarshal([]byte(payload), &claim); err != nil {
		return nil, err
	}
	return &claim, nil
}

func decodeFleetOwnedDevice(row *sql.Row) (*spec.FleetOwnedDevice, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var device spec.FleetOwnedDevice
	if err := json.Unmarshal([]byte(payload), &device); err != nil {
		return nil, err
	}
	return &device, nil
}

func decodeFleetOwnedNode(row *sql.Row) (*spec.FleetOwnedNode, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var node spec.FleetOwnedNode
	if err := json.Unmarshal([]byte(payload), &node); err != nil {
		return nil, err
	}
	return &node, nil
}

func decodeFleetNodeAuthState(row *sql.Row) (*spec.FleetNodeAuthState, error) {
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var state spec.FleetNodeAuthState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func scanFleetPendingClaims(rows *sql.Rows) ([]spec.FleetPendingClaim, error) {
	var claims []spec.FleetPendingClaim
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var claim spec.FleetPendingClaim
		if err := json.Unmarshal([]byte(payload), &claim); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func scanFleetOwnedDevices(rows *sql.Rows) ([]spec.FleetOwnedDevice, error) {
	var devices []spec.FleetOwnedDevice
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var device spec.FleetOwnedDevice
		if err := json.Unmarshal([]byte(payload), &device); err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func scanFleetOwnedNodes(rows *sql.Rows) ([]spec.FleetOwnedNode, error) {
	var nodes []spec.FleetOwnedNode
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var node spec.FleetOwnedNode
		if err := json.Unmarshal([]byte(payload), &node); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
