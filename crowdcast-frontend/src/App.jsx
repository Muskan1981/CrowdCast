import React, { useEffect, useState } from 'react';
import Sidebar from './components/Sidebar';
import Topbar from './components/Topbar';
import Toast from './components/Toast';
import OverviewPage from './pages/OverviewPage';
import LiveEventPage from './pages/LiveEventPage';
import InfrastructurePage from './pages/InfrastructurePage';
import AudiencePage from './pages/AudiencePage';

const initialMessages = [
  { id: 1, name: 'Aarav Mehta', text: 'The visuals are INSANE 🔥', time: 'now', color: 'mint' },
  { id: 2, name: 'Ishita Rao', text: 'This is my favourite track!', time: '1m', color: 'peach' },
  { id: 3, name: 'Dev Shah', text: 'Hello from Mumbai 👋', time: '1m', color: 'lilac' },
  { id: 4, name: 'Naina Kapoor', text: 'Can we get an encore?', time: '2m', color: 'yellow' },
];

export default function App() {
  const [page, setPage] = useState('Overview');
  const [eventLive, setEventLive] = useState(true);
  const [attendees, setAttendees] = useState(12840);
  const [reactions, setReactions] = useState(28491);
  const [messages, setMessages] = useState(initialMessages);
  const [chatText, setChatText] = useState('');
  const [selectedPoll, setSelectedPoll] = useState(null);
  const [pollVotes, setPollVotes] = useState([54, 31, 15]);
  const [toast, setToast] = useState('');
  const [serverDown, setServerDown] = useState(false);
  const [mobileNav, setMobileNav] = useState(false);
  const [reactionBurst, setReactionBurst] = useState('');
  const [muted, setMuted] = useState(false);

  useEffect(() => {
    if (!eventLive || serverDown) return undefined;
    const timer = setInterval(() => {
      setAttendees(value => value + Math.floor(Math.random() * 5));
      setReactions(value => value + Math.floor(Math.random() * 9));
    }, 4500);
    return () => clearInterval(timer);
  }, [eventLive, serverDown]);

  useEffect(() => {
    if (!toast) return undefined;
    const timer = setTimeout(() => setToast(''), 2400);
    return () => clearTimeout(timer);
  }, [toast]);

  const showToast = message => setToast(message);

  const sendMessage = event => {
    event.preventDefault();
    if (!chatText.trim()) return;
    setMessages(previous => [
      ...previous,
      { id: Date.now(), name: 'You', text: chatText.trim(), time: 'now', color: 'mint' },
    ].slice(-7));
    setChatText('');
  };

  const sendReaction = emoji => {
    setReactions(value => value + 1);
    setReactionBurst(emoji);
    setTimeout(() => setReactionBurst(''), 850);
  };

  const vote = index => {
    if (selectedPoll !== null) return;
    setSelectedPoll(index);
    setPollVotes(previous => previous.map((value, i) => i === index ? value + 1 : value));
    showToast('Your vote has been counted');
  };

  const pageProps = {
    attendees,
    reactions,
    messages,
    showToast,
  };

  const renderPage = () => {
    switch (page) {
      case 'Live event':
        return (
          <LiveEventPage
            {...pageProps}
            eventLive={eventLive}
            setEventLive={setEventLive}
            chatText={chatText}
            setChatText={setChatText}
            sendMessage={sendMessage}
            selectedPoll={selectedPoll}
            pollVotes={pollVotes}
            vote={vote}
            muted={muted}
            setMuted={setMuted}
            reactionBurst={reactionBurst}
            sendReaction={sendReaction}
          />
        );
      case 'Infrastructure':
        return <InfrastructurePage serverDown={serverDown} setServerDown={setServerDown} showToast={showToast} />;
      case 'Audience':
        return <AudiencePage {...pageProps} />;
      case 'Overview':
      default:
        return (
          <OverviewPage
            {...pageProps}
            eventLive={eventLive}
            serverDown={serverDown}
            setPage={setPage}
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
            <span>v1.0.0 · Demo environment</span>
          </footer>
        </div>
      </main>

      {mobileNav && <div className="mobile-scrim" onClick={() => setMobileNav(false)} />}
      <Toast message={toast} onClose={() => setToast('')} />
    </div>
  );
}
