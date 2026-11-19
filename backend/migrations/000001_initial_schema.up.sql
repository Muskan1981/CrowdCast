-- Events table
CREATE TABLE IF NOT EXISTS events (
    id VARCHAR(64) PRIMARY KEY,
    event_number VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    title_main VARCHAR(128) NOT NULL,
    subtitle VARCHAR(128) NOT NULL,
    session_code VARCHAR(64) NOT NULL,
    description TEXT NOT NULL,
    host_name VARCHAR(128) NOT NULL,
    host_avatar_letter VARCHAR(8) NOT NULL,
    stage_name VARCHAR(128) NOT NULL,
    stage_resolution VARCHAR(64) NOT NULL,
    date_eyebrow VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'scheduled',
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    paused_ms BIGINT NOT NULL DEFAULT 0,
    peak_audience INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Polls table
CREATE TABLE IF NOT EXISTS polls (
    id VARCHAR(64) PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    question TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_polls_event_status ON polls(event_id, status);

-- Poll Options table
CREATE TABLE IF NOT EXISTS poll_options (
    id BIGSERIAL PRIMARY KEY,
    poll_id VARCHAR(64) NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    option_index INT NOT NULL,
    option_text TEXT NOT NULL,
    vote_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_poll_option UNIQUE (poll_id, option_index)
);

-- Poll Votes table (One vote per user per poll enforced by PK / UNIQUE)
CREATE TABLE IF NOT EXISTS poll_votes (
    poll_id VARCHAR(64) NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    user_id VARCHAR(128) NOT NULL,
    option_index INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (poll_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_poll_votes_poll_id ON poll_votes(poll_id);

-- Viewers table
CREATE TABLE IF NOT EXISTS viewers (
    id VARCHAR(128) PRIMARY KEY,
    display_name VARCHAR(128) NOT NULL,
    city VARCHAR(128) NOT NULL,
    color VARCHAR(32) NOT NULL DEFAULT 'mint',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Attendance sessions table
CREATE TABLE IF NOT EXISTS attendance_sessions (
    id BIGSERIAL PRIMARY KEY,
    viewer_id VARCHAR(128) NOT NULL REFERENCES viewers(id) ON DELETE CASCADE,
    event_id VARCHAR(64) NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    left_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_attendance_event_joined ON attendance_sessions(event_id, joined_at);

-- Chat messages table
CREATE TABLE IF NOT EXISTS chat_messages (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id VARCHAR(128) NOT NULL,
    user_name VARCHAR(128) NOT NULL,
    user_color VARCHAR(32) NOT NULL DEFAULT 'mint',
    text VARCHAR(280) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_chat_event_created ON chat_messages(event_id, created_at DESC);

-- Reaction rollups table
CREATE TABLE IF NOT EXISTS reaction_rollups (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    minute_bucket TIMESTAMPTZ NOT NULL,
    emoji VARCHAR(16) NOT NULL,
    count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_reaction_rollup UNIQUE (event_id, minute_bucket, emoji)
);
CREATE INDEX IF NOT EXISTS idx_reaction_rollups_event_bucket ON reaction_rollups(event_id, minute_bucket DESC);

-- Activity log table
CREATE TABLE IF NOT EXISTS activity_log (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    activity_type VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT NOT NULL,
    icon VARCHAR(64) NOT NULL DEFAULT 'Users',
    tone VARCHAR(32) NOT NULL DEFAULT 'mint',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_activity_event_created ON activity_log(event_id, created_at DESC);
