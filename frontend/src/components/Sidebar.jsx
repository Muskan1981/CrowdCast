import React from 'react';
import {
  AudioLines, X, ChevronDown, ChevronRight, LayoutDashboard, Radio,
  Server, Users, Ticket, BarChart3, Settings, Sparkles, MoreHorizontal
} from 'lucide-react';

const navItems = [
  { label: 'Overview', icon: LayoutDashboard },
  { label: 'Live event', icon: Radio, dot: true },
  { label: 'Infrastructure', icon: Server },
  { label: 'Audience', icon: Users },
];

export default function Sidebar({ page, mobileNav, setPage, setMobileNav, showToast }) {
  return (
    <aside className={`sidebar ${mobileNav ? 'sidebar-open' : ''}`}>
      <div className="brand">
        <div className="brand-mark"><AudioLines size={21} /></div>
        <span>crowd<span className="brand-light">cast</span><sup>LIVE</sup></span>
        <button className="mobile-close" onClick={() => setMobileNav(false)}><X size={18} /></button>
      </div>
      <div className="workspace-label">WORKSPACE</div>
      <button className="workspace-switch" onClick={() => showToast('You are in the main workspace')}>
        <div className="workspace-avatar">S</div>
        <span><b>Studio Sessions</b><small>Free workspace</small></span>
        <ChevronDown size={15} />
      </button>
      <div className="nav-label">MAIN MENU</div>
      <nav className="nav-list">
        {navItems.map(({ label, icon: Icon, dot }) => (
          <button key={label} className={`nav-item ${page === label ? 'active' : ''}`} onClick={() => { setPage(label); setMobileNav(false); }}>
            <Icon size={18} /><span>{label}</span>{dot && <i className="live-dot" />}{label === 'Audience' && <span className="nav-count">12.8k</span>}
          </button>
        ))}
      </nav>
      <div className="nav-label tools-label">MANAGE</div>
      <nav className="nav-list">
        <button className="nav-item" onClick={() => showToast('Event library is coming soon')}><Ticket size={18} /><span>Event library</span></button>
        <button className="nav-item" onClick={() => showToast('Reports are coming soon')}><BarChart3 size={18} /><span>Reports</span></button>
        <button className="nav-item" onClick={() => showToast('Settings are coming soon')}><Settings size={18} /><span>Settings</span></button>
      </nav>
      <div className="sidebar-bottom">
        <div className="help-card">
          <div className="help-icon"><Sparkles size={17} /></div>
          <b>Need a hand?</b>
          <p>Explore the CrowdCast quick-start guide.</p>
          <button onClick={() => showToast('Quick-start guide opened')}>Open guide <ChevronRight size={13} /></button>
        </div>
        <div className="profile">
          <div className="profile-avatar">MA</div>
          <div className="profile-info"><b>Muskan Agarwal</b><small>Event admin</small></div>
          <MoreHorizontal size={19} />
        </div>
      </div>
    </aside>
  );
}
