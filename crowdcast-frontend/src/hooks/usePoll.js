import { useCallback, useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { EVENT_ID, USE_MOCKS } from '../config';
import { mockPoll } from '../data/mockState';
import { useSocketEvent } from './useRealtime';

const storageKey = (pollId) => `crowdcast.vote.${pollId}`;

function readVote(pollId) {
  try {
    const raw = window.localStorage.getItem(storageKey(pollId));
    return raw === null ? null : Number(raw);
  } catch {
    return null;
  }
}

function writeVote(pollId, option) {
  try {
    window.localStorage.setItem(storageKey(pollId), String(option));
  } catch {
    /* storage unavailable: the choice is kept for this page view only */
  }
}

/**
 * usePoll returns the active poll with counts (not percentages) and the total.
 * The vote is cast once per poll; the chosen option is remembered locally
 * because the API does not echo the viewer's own vote.
 */
export function usePoll(socket, token) {
  const [poll, setPoll] = useState(USE_MOCKS ? mockPoll : null);
  const [selected, setSelected] = useState(() => (USE_MOCKS ? readVote(mockPoll.id) : null));
  const [error, setError] = useState(null);

  const refresh = useCallback(() => {
    if (USE_MOCKS) return;
    apiFetch(`/events/${EVENT_ID}/polls/active`)
      .then((data) => {
        setPoll(data);
        setSelected(readVote(data.id));
      })
      .catch((err) => setError(err));
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  useSocketEvent(socket, 'welcome', (frame) => {
    if (frame.active_poll) {
      setPoll(frame.active_poll);
      setSelected(readVote(frame.active_poll.id));
    }
  });
  useSocketEvent(socket, 'poll_update', (frame) => {
    setPoll((prev) => (prev && prev.id === frame.poll_id
      ? { ...prev, votes: frame.votes, percentages: frame.percentages, total_votes: frame.total_votes }
      : prev));
  });

  /** Casts the vote for option index. Returns the updated poll, or null if already voted. */
  const vote = async (index) => {
    if (!poll || selected !== null) return null;
    if (USE_MOCKS) {
      setSelected(index);
      return null;
    }
    try {
      const result = await apiFetch(`/polls/${poll.id}/vote`, { method: 'POST', token, body: { option: index } });
      writeVote(poll.id, index);
      setSelected(index);
      setPoll((prev) => ({ ...prev, votes: result.votes, percentages: result.percentages, total_votes: result.total_votes }));
      return result;
    } catch (err) {
      if (err.status === 409) {
        // The server already counts a vote from this viewer, so remember it.
        writeVote(poll.id, index);
        setSelected(index);
        return null;
      }
      setError(err);
      throw err;
    }
  };

  return {
    question: poll?.question || '',
    options: poll?.options || [],
    counts: poll?.votes || [],
    percentages: poll?.percentages || [],
    total: poll?.total_votes || 0,
    selected,
    vote,
    error,
  };
}
