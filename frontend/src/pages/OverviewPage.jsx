import React from 'react';
import {
  Activity, AudioLines, BarChart3, Check, ChevronRight, Copy, Cpu, Gauge,
  Heart, MessageCircle, MoreHorizontal, Pause, Play, Radio, Send, Settings,
  ShieldCheck, Server, ArrowDownRight, ArrowUpRight, MonitorPlay, Users,
  Wifi, Zap, Flame, PartyPopper, Signal, Cloud, CircleHelp, Smile, ChevronDown
} from 'lucide-react';
import {
  Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip,
  BarChart, Bar, XAxis, YAxis
} from 'recharts';
import MetricCard from '../components/MetricCard';
import StatusPill from '../components/StatusPill';
import { trafficData, serverRows } from '../data/dashboardData';

export default function OverviewPage({ attendees, reactions, eventLive, serverDown, setPage, showToast }) {
  return (
    <>

          <section className="page-heading"><div><div className="eyebrow"><span className="eyebrow-line"/> TUESDAY, NOVEMBER 17, 2026</div><h1>Your event, <em>in motion.</em></h1><p className="subheading">Here's what's happening across your CrowdCast experience.</p></div><div className="heading-actions"><button className="button-secondary" onClick={() => showToast('Share link copied to clipboard')}><Copy size={15}/> Share event</button><button className="button-primary" onClick={() => setPage('Live event')}><Radio size={16}/> Open live room</button></div></section>

          <section className="event-banner"><div className="event-art"><div className="art-orbit orbit-one"/><div className="art-orbit orbit-two"/><div className="art-sun"/><div className="art-word">AFTER<br/>HOURS<span>SESSION 08</span></div><div className="art-equalizer"><i/><i/><i/><i/><i/><i/><i/><i/><i/><i/><i/></div></div><div className="event-info"><div className="event-kicker"><span className="live-tag"><i/>{eventLive ? 'HAPPENING NOW' : 'PAUSED'}</span><span className="event-id">EVENT #CC-0826</span></div><h2>After Hours <span>—</span><br/>The Sound of Now</h2><p className="event-description">An immersive night of electronic sounds, visual art, and people who get it.</p><div className="event-meta"><span><Users size={15}/> {attendees.toLocaleString('en-IN')} attending</span><span><span className="meta-divider"/> <span className="event-host-avatar">A</span> Hosted by Aanya Verma</span></div><div className="event-actions"><button className="button-lime" onClick={() => setPage('Live event')}><MonitorPlay size={16}/> Enter live room <ChevronRight size={15}/></button><button className="event-text-btn" onClick={() => showToast('Event details copied')}>Event details <ArrowUpRight size={14}/></button></div></div><div className="event-side"><div className="side-stat"><span>LIVE FOR</span><b>01:42:18</b></div><div className="side-stat"><span>PEAK AUDIENCE</span><b>13,204</b></div><div className="side-wave"><AudioLines size={40}/><span>LIVE AUDIO</span></div></div></section>

          <section className="metrics-grid">
            <MetricCard label="Live attendees" value={attendees.toLocaleString('en-IN')} change="12.8%" foot="vs. last event" icon={Users} tone="mint"/>
            <MetricCard label="Reactions sent" value={reactions.toLocaleString('en-IN')} change="8.4%" foot="in this session" icon={Heart} tone="coral"/>
            <MetricCard label="Avg. response time" value="42 ms" change="6.2%" foot="faster than target" icon={Gauge} tone="lilac"/>
            <MetricCard label="Requests / sec" value={serverDown ? '842' : '1,284'} change="18.6%" foot="across all nodes" icon={Zap} tone="yellow"/>
          </section>

          <section className="dashboard-grid">
            <div className="panel traffic-panel"><div className="panel-header"><div><div className="panel-overline">LIVE PERFORMANCE</div><h3>Traffic overview</h3></div><button className="select-button" onClick={() => showToast('Showing the last 60 minutes')}>Last 60 min <ChevronDown size={14}/></button></div><div className="chart-summary"><b>12,840 <span>req/min</span></b><span className="chart-change"><ArrowUpRight size={14}/> 18.6%</span><small>Requests served across all instances</small></div><div className="traffic-chart"><ResponsiveContainer width="100%" height="100%"><AreaChart data={trafficData} margin={{top: 12,right: 4,left: -22,bottom: 0}}><defs><linearGradient id="trafficFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#c9f36a" stopOpacity={0.28}/><stop offset="100%" stopColor="#c9f36a" stopOpacity={0}/></linearGradient></defs><CartesianGrid stroke="#292d26" strokeDasharray="3 5" vertical={false}/><XAxis dataKey="time" tick={{fill:'#73796d',fontSize:10}} axisLine={false} tickLine={false} interval={2}/><YAxis tick={{fill:'#73796d',fontSize:10}} axisLine={false} tickLine={false}/><Tooltip contentStyle={{background:'#20241e',border:'1px solid #3a4033',borderRadius:10,color:'#f3f2e9',fontSize:12}}/><Area type="monotone" dataKey="requests" stroke="#c9f36a" strokeWidth={2.5} fill="url(#trafficFill)" activeDot={{r:5,fill:'#c9f36a',stroke:'#151812',strokeWidth:2}}/></AreaChart></ResponsiveContainer></div><div className="chart-legend"><span><i className="legend-lime"/> Incoming requests</span><span className="chart-live"><i/> Updating live</span></div></div>

            <div className="panel distribution-panel"><div className="panel-header"><div><div className="panel-overline">LOAD BALANCER</div><h3>Traffic distribution</h3></div><span className="tiny-live"><i/> LIVE</span></div><div className="distribution-total"><b>12,840</b><span>total requests</span></div><div className="stacked-bar"><i style={{width:'50%'}}/><i style={{width:'30%'}}/><i style={{width:'20%'}}/></div><div className="server-legend">{serverRows.map(s => <div className="server-legend-row" key={s.name}><span className={`legend-dot ${s.color}`}/><span className="server-legend-name">{s.name.replace('crowdcast-api-', 'Node ')}</span><b>{s.weight}%</b></div>)}</div><div className="distribution-note"><ShieldCheck size={16}/><span>Weighted routing is distributing traffic according to node capacity.</span></div><button className="panel-link" onClick={() => setPage('Infrastructure')}>View infrastructure <ArrowUpRight size={14}/></button></div>
          </section>

          <section className="bottom-grid"><div className="panel nodes-panel"><div className="panel-header"><div><div className="panel-overline">FLEET HEALTH</div><h3>Backend instances</h3></div><button className="panel-link" onClick={() => setPage('Infrastructure')}>View all <ArrowUpRight size={14}/></button></div><div className="node-table"><div className="node-table-head"><span>INSTANCE</span><span>CPU</span><span>MEMORY</span><span>STATUS</span></div>{serverRows.map((s, i) => <div className={`node-row ${serverDown && i === 1 ? 'node-failed' : ''}`} key={s.name}><div className="node-name"><span className={`node-symbol ${s.color}`}><Server size={15}/></span><span><b>{s.name}</b><small>{s.zone}</small></span></div><div className="mini-meter"><span>{serverDown && i === 1 ? '—' : `${s.cpu}%`}</span><i><b style={{width:serverDown && i === 1 ? '0%' : `${s.cpu}%`}}/></i></div><div className="mini-meter"><span>{serverDown && i === 1 ? '—' : `${s.memory}%`}</span><i><b style={{width:serverDown && i === 1 ? '0%' : `${s.memory}%`}}/></i></div><StatusPill good={!(serverDown && i === 1)}>{serverDown && i === 1 ? 'Offline' : 'Healthy'}</StatusPill></div>)}</div></div>
          <div className="panel activity-panel"><div className="panel-header"><div><div className="panel-overline">WHAT'S HAPPENING</div><h3>Recent activity</h3></div><button className="more-button" onClick={() => showToast('You are viewing the latest activity')}><MoreHorizontal size={19}/></button></div><div className="activity-list"><div className="activity-item"><span className="activity-icon mint"><Users size={15}/></span><div><b>Audience milestone reached</b><p>12,000 attendees joined the room</p><small>2 minutes ago</small></div></div><div className="activity-item"><span className="activity-icon peach"><Zap size={15}/></span><div><b>Traffic spike detected</b><p>Requests peaked at 1.4k req/s</p><small>8 minutes ago</small></div></div><div className="activity-item"><span className="activity-icon lilac"><ShieldCheck size={15}/></span><div><b>All systems operational</b><p>Health checks passed on 3 nodes</p><small>12 minutes ago</small></div></div></div></div></section>
        
    </>
  );
}
