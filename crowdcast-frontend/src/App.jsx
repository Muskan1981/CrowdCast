import React, { useEffect, useMemo, useState } from 'react';
import Sidebar from './components/Sidebar';
import Topbar from './components/Topbar';
import Toast from './components/Toast';
import OverviewPage from './pages/OverviewPage';
import LiveEventPage from './pages/LiveEventPage';
import InfrastructurePage from './pages/InfrastructurePage';
import AudiencePage from './pages/AudiencePage';
import { EVENT_ID, USE_MOCKS } from './config';
import { useSession } from './hooks/useSession';
import { useRealtime } from './hooks/useRealtime';
import { useEventState } from './hooks/useEventState';
import { usePresence } from './hooks/usePresence';
import { useChat } from './hooks/useChat';
import { usePoll } from './hooks/usePoll';
import { useReactions } from './hooks/useReactions';
import { useOverviewStats } from './hooks/useOverviewStats';
import { useTrafficSeries } from './hooks/useTrafficSeries';
import { useInfraNodes } from './hooks/useInfraNodes';
import { useAudienceStats } from './hooks/useAudienceStats';
import { ApiError } from './api/client';

const TARGET_NODE = 'crowdcast-api-02';

// splitTitle turns "After Hours — The Sound of Now" into the two lines the banner shows.
function splitTitle(title = '') {
  const [left, ...rest] = title.split(' — ');
  return { left: left || 'After Hours', right: rest.join(' — ') || 'The Sound of Now' };
}

export default function App() {
  const [page, setPage] = useState('Overview');
  const [toast, setToast] = useState('');
  const [mobileNav, setMobileNav] = useState(false);
  const [reactionBurst, setReactionBurst] = useState('');
  const [muted, setMuted] = useState(false);
  const [chatText, setChatText] = useState('');
  const [actionError, setActionError] = useState('');

  // Session and realtime connection for this tab.
  const session = useSession();
  const realtime = useRealtime(EVENT_ID, session.token);
  const socket = realtime.socket;

  // Data hooks. Each one reads the API and follows the socket where it helps.
  const eventState = useEventState(socket);
  const presence = usePresence(socket);
  const chat = useChat(socket, session.token, session.viewer?.display_name);
  const poll = usePoll(socket, session.token);
  const reactions = useReactions(socket, session.token);
  const overview = useOverviewStats(socket);
  const traffic = useTrafficSeries('60m');
  const infra = useInfraNodes(socket);
  const audience = useAudienceStats();

  // Toasts for the connection state, as the original UI already had.
  useEffect(() => {
    if (realtime.state === 'reconnecting') setToast('Reconnecting...');
  }, [realtime.state]);
  useEffect(() => {
    const off = socket?.on('open', ({ reconnected }) => {
      if (reconnected) setToast('Back online');
    });
    return () => off && off();
  }, [socket]);

  useEffect(() => {
    if (!toast) return undefined;
    const timer = setTimeout(() => setToast(''), 2400);
    return () => clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    if (!actionError) return undefined;
    const timer = setTimeout(() => setActionError(''), 4000);
    return () => clearTimeout(timer);
  }, [actionError]);

  const showToast = message => setToast(message);

  const reportError = (err, fallback) => {
    if (err instanceof ApiError && err.status === 401) session.reset();
    setActionError(err?.message || fallback);
    setToast(err?.message || fallback);
  };

  // Derived values the pages render.
  const attendees = presence.attendees || overview.data?.metrics?.live_attendees?.raw || 0;
  const eventLive = eventState.eventLive;
  const event = eventState.event || {};
  const { left: titleLeft, right: titleRight } = splitTitle(event.title);

  const nodes = infra.fleet.server_rows || [];
  const api02 = nodes.find(n => n.name === TARGET_NODE);
  const serverDown = !!api02 && api02.status !== 'Healthy';
  const shares = useMemo(() => {
    const byNode = {};
    (overview.data?.distribution || []).forEach(d => { byNode[d.instance] = d.share_pct; });
    return byNode;
  }, [overview.data]);
  const totalLastHour = traffic.points.reduce((sum, p) => sum + (p.requests || 0), 0);

  const toggleLive = async () => {
    try {
      await eventState.lifecycle(eventLive ? 'pause' : 'resume');
      showToast(eventLive ? 'Broadcast paused' : 'Broadcast resumed');
    } catch (err) {
      reportError(err, 'Could not change the broadcast state');
    }
  };

  const endStream = async () => {
    try {
      await eventState.lifecycle('end');
      showToast('Stream ended');
    } catch (err) {
      reportError(err, 'Could not end the stream');
    }
  };

  const sendMessage = async event => {
    event.preventDefault();
    if (!chatText.trim()) return;
    const text = chatText;
    setChatText('');
    try {
      await chat.send(text);
    } catch (err) {
      setChatText(text);
      reportError(err, 'Message not sent');
    }
  };

  const sendReaction = async emoji => {
    setReactionBurst(emoji);
    setTimeout(() => setReactionBurst(''), 850);
    try {
      await reactions.send(emoji);
    } catch (err) {
      reportError(err, 'Reaction not sent');
    }
  };

  const vote = async index => {
    try {
      const result = await poll.vote(index);
      if (result !== null || poll.selected === null) showToast('Your vote has been counted');
    } catch (err) {
      reportError(err, 'Vote not counted');
    }
  };

  const toggleNode = async node => {
    try {
      const action = await infra.toggle(node);
      showToast(action === 'fail' ? `${node.name} taken offline for failure test` : `${node.name} restored`);
    } catch (err) {
      reportError(err, 'Node action failed');
    }
  };

  const pageProps = {
    attendees,
    reactions: reactions.total,
    messages: chat.messages,
    showToast,
  };

  const renderPage = () => {
    switch (page) {
      case 'Live event':
        return (
          <LiveEventPage
            {...pageProps}
            eventLive={eventLive}
            toggleLive={toggleLive}
            endStream={endStream}
            chatTotal={chat.total}
            chatText={chatText}
            setChatText={setChatText}
            sendMessage={sendMessage}
            question={poll.question}
            options={poll.options}
            percentages={poll.percentages}
            pollTotal={poll.total}
            selectedPoll={poll.selected}
            vote={vote}
            muted={muted}
            setMuted={setMuted}
            reactionBurst={reactionBurst}
            sendReaction={sendReaction}
            presenceDelta={presence.delta}
            bars={presence.bars}
            stage={event.stage}
          />
        );
      case 'Infrastructure':
        return (
          <InfrastructurePage
            nodes={nodes}
            fleet={infra.fleet}
            serverDown={serverDown}
            toggleNode={() => api02 && toggleNode(api02)}
            pendingNode={infra.pending}
            refresh={infra.refresh}
            showToast={showToast}
          />
        );
      case 'Audience':
        return <AudiencePage {...pageProps} audience={audience.data} />;
      case 'Overview':
      default:
        return (
          <OverviewPage
            {...pageProps}
            eventLive={eventLive}
            serverDown={serverDown}
            setPage={setPage}
            event={event}
            titleLeft={titleLeft}
            titleRight={titleRight}
            metrics={overview.data?.metrics}
            activity={overview.data?.activity || []}
            traffic={traffic.points}
            trafficTotal={totalLastHour}
            nodes={nodes}
            shares={shares}
            fleet={infra.fleet}
          />
        );
    }
  };

  return (
    <div className="app-shell">
      <Sidebar
        page={page}
        mobileNav={mobileNav}
        setPage={setPage}
        setMobileNav={setMobileNav}
        showToast={showToast}
      />

      <main className="main-content">
        <Topbar page={page} setMobileNav={setMobileNav} showToast={showToast} />
        <div className="page-wrap">
          {renderPage()}
          <footer className="footer">
            <span>© 2026 CrowdCast Studio</span>
            <span><span className="footer-dot" /> Built for moments that matter.</span>
            <span>v1.0.0 · {USE_MOCKS ? 'Demo data' : `Live · ${realtime.state}`}</span>
          </footer>
        </div>
      </main>

      {mobileNav && <div className="mobile-scrim" onClick={() => setMobileNav(false)} />}
      <Toast message={toast} onClose={() => setToast('')} />
    </div>
  );
}
