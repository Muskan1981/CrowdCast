// Demo values used when VITE_USE_MOCKS=true, and as the first paint before the
// API responds. These match the numbers that were hard-coded in the original UI.
export const mockOverview = {
  event: { id: 'cc-0826', title: 'After Hours — The Sound of Now', status: 'live', live_for_formatted: '01:42:18', live_for_seconds: 6138, peak_audience: 13204 },
  metrics: {
    live_attendees: { value: '12,840', raw: 12840, change: '12.8%', foot: 'vs. last event', tone: 'up' },
    reactions_sent: { value: '28,491', raw: 28491, change: '8.4%', foot: 'in this session', tone: 'up' },
    avg_response_time: { value: '42 ms', raw: 42, change: '6.2%', foot: 'faster than target', tone: 'up' },
    requests_per_sec: { value: '1,284/s', raw: 1284, change: '18.6%', foot: 'across all nodes', tone: 'up' },
  },
  traffic_summary: { source: 'mock', requests_per_sec: 1284 },
  distribution: [],
  activity: [
    { id: 1, title: 'Audience milestone reached', description: '12,000 attendees joined the room', time_ago: '2 minutes ago', icon: 'Users', tone: 'mint' },
    { id: 2, title: 'Traffic spike detected', description: 'Requests peaked at 1.4k req/s', time_ago: '8 minutes ago', icon: 'Zap', tone: 'peach' },
    { id: 3, title: 'All systems operational', description: 'Health checks passed on 3 nodes', time_ago: '12 minutes ago', icon: 'ShieldCheck', tone: 'lilac' },
  ],
};

export const mockAudience = {
  metrics: {
    total_attendees: { value: '12,840', raw: 12840, change: '12.8%', foot: 'vs. last event', tone: 'up' },
    returning_audience: { value: '68.4%', raw: 68, change: '4.2%', foot: 'loyal listeners', tone: 'up' },
    avg_watch_time: { value: '48 min', raw: 48, change: '11.2%', foot: 'per attendee', tone: 'up' },
    chat_messages: { value: '3,842', raw: 3842, change: '22.1%', foot: 'this session', tone: 'up' },
  },
  city_mix: [
    { name: 'Mumbai', percentage: '28%' }, { name: 'Delhi NCR', percentage: '24%' },
    { name: 'Bengaluru', percentage: '19%' }, { name: 'Hyderabad', percentage: '14%' },
    { name: 'Other cities', percentage: '15%' },
  ],
  top_reactions: [
    { emoji: '🔥', name: 'Fire', subtitle: 'Most used', count: '8,420' },
    { emoji: '❤️', name: 'Heart', subtitle: 'Second', count: '6,180' },
    { emoji: '👏', name: 'Applause', subtitle: 'Third', count: '4,920' },
  ],
};

export const mockPoll = {
  id: 'poll-01',
  question: "What's the energy level right now?",
  options: ['Absolutely electric ⚡', 'In the zone ✨', 'Just warming up 🌱'],
  votes: [540, 310, 150],
  percentages: [54, 31, 15],
  total_votes: 1000,
};

export const mockChat = [
  { id: 1, name: 'Aarav Mehta', text: 'The visuals are INSANE 🔥', time: 'now', color: 'mint' },
  { id: 2, name: 'Ishita Rao', text: 'This is my favourite track!', time: '1m', color: 'peach' },
  { id: 3, name: 'Dev Shah', text: 'Hello from Mumbai 👋', time: '1m', color: 'lilac' },
  { id: 4, name: 'Naina Kapoor', text: 'Can we get an encore?', time: '2m', color: 'yellow' },
];
