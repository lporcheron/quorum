-- name: UpsertVote :exec
INSERT INTO votes (participant_id, option_id, value, updated_at)
VALUES (@participant_id, @option_id, @value, @updated_at)
ON CONFLICT (participant_id, option_id)
DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: DeleteParticipantVotes :exec
DELETE FROM votes WHERE participant_id = @participant_id;

-- name: ListPollVotes :many
SELECT v.participant_id, v.option_id, v.value
FROM votes v
JOIN participants p ON p.id = v.participant_id
WHERE p.poll_id = @poll_id;
