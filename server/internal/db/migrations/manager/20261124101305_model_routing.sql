-- model_routing: one row per decision of which candidate of a model profile's tier (an Agent preset) a dispatch
-- runs on (#14700). Rows of the profile in use (active) are the dispatches that ran; rows of a profile not in use are
-- trials that started nothing (#14702). detail holds each candidate as the pick judged it.
CREATE TABLE model_routing (
    id bigserial PRIMARY KEY,
    at timestamptz NOT NULL,
    todo_id text NOT NULL DEFAULT '',
    plan_id text NOT NULL DEFAULT '',
    task_index int NOT NULL DEFAULT 0,
    session_id text NOT NULL DEFAULT '',
    profile_id text NOT NULL,
    active bool NOT NULL,
    tier text NOT NULL,
    candidate int NOT NULL,
    of int NOT NULL,
    launcher text NOT NULL,
    model text NOT NULL DEFAULT '',
    effort text NOT NULL DEFAULT '',
    reason text NOT NULL,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX model_routing_at ON model_routing (at);
CREATE INDEX model_routing_todo ON model_routing (todo_id) WHERE todo_id <> '';
CREATE INDEX model_routing_profile ON model_routing (profile_id, at);
