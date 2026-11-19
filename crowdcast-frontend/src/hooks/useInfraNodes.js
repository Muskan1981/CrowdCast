import { useCallback, useEffect, useState } from 'react';
import { apiFetch } from '../api/client';
import { USE_MOCKS } from '../config';
import { serverRows } from '../data/dashboardData';
import { useSocketEvent } from './useRealtime';

const POLL_MS = 2000;

const emptyFleet = {
  fleet_status: 'Operational',
  degraded: false,
  metrics: { total_requests: 0, avg_latency_ms: 0, error_rate_pct: 0, active_connections: 0 },
  server_rows: [],
  monitoring_services: [],
};

/**
 * useInfraNodes polls /infra/nodes every 2 s and refetches on node_state, so a
 * chaos change shows up on every open tab. toggle() fails or restores a node.
 */
export function useInfraNodes(socket) {
  const [fleet, setFleet] = useState(USE_MOCKS ? { ...emptyFleet, server_rows: serverRows } : emptyFleet);
  const [error, setError] = useState(null);
  const [pending, setPending] = useState('');

  const refresh = useCallback(() => {
    if (USE_MOCKS) return Promise.resolve();
    return apiFetch('/infra/nodes')
      .then((d) => { setFleet(d); setError(null); })
      .catch((err) => setError(err));
  }, []);

  useEffect(() => {
    refresh();
    if (USE_MOCKS) return undefined;
    const timer = setInterval(refresh, POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  useSocketEvent(socket, 'node_state', () => {
    refresh();
  });

  /** node is a server row. Healthy nodes are failed; offline or failed nodes are restored. */
  const toggle = async (node) => {
    const action = node.status === 'Healthy' ? 'fail' : 'restore';
    setPending(node.name);
    try {
      await apiFetch(`/admin/nodes/${node.name}/${action}`, { method: 'POST', admin: true });
    } finally {
      setPending('');
      refresh();
    }
    return action;
  };

  return { fleet, error, toggle, pending, refresh };
}
