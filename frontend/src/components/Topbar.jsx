import React from 'react';
import { Menu, ChevronRight, Bell } from 'lucide-react';

export default function Topbar({ page, setMobileNav, showToast }) {
  return (
    <header className="topbar">
      <div className="topbar-left">
        <button className="hamburger" onClick={() => setMobileNav(true)}><Menu size={20} /></button>
        <span className="breadcrumb">Workspace <ChevronRight size={14} /> <b>{page}</b></span>
      </div>
      <div className="topbar-right">
        <div className="system-status"><span className="pulse-dot" /> All systems operational</div>
        <button className="icon-button" onClick={() => showToast('You are all caught up')} aria-label="Notifications"><Bell size={18} /><i /></button>
        <div className="top-avatar">MA</div>
      </div>
    </header>
  );
}
