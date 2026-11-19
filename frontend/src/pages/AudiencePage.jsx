import React from 'react';
import {
  Activity, AudioLines, BarChart3, Check, ChevronRight, Copy, Cpu, Gauge,
  Heart, MessageCircle, MoreHorizontal, Pause, Play, Radio, Send, Settings,
  ShieldCheck, Server, ArrowDownRight, ArrowUpRight, MonitorPlay, Users,
  Wifi, Zap, Flame, PartyPopper, Signal, Cloud, CircleHelp, Smile
} from 'lucide-react';
import {
  Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip,
  BarChart, Bar, XAxis, YAxis
} from 'recharts';
import MetricCard from '../components/MetricCard';
import StatusPill from '../components/StatusPill';
import { trafficData, serverRows } from '../data/dashboardData';

export default function AudiencePage({ attendees, reactions, messages, showToast }) {
  return (
    <>

          <section className="page-heading"><div><div className="eyebrow"><span className="eyebrow-line"/> YOUR COMMUNITY</div><h1>People make the <em>moment.</em></h1><p className="subheading">A snapshot of the crowd showing up for After Hours.</p></div><div className="heading-actions"><button className="button-secondary" onClick={()=>showToast('Audience report exported (demo)')}><BarChart3 size={15}/> Export report</button></div></section>
          <section className="metrics-grid"><MetricCard label="Total attendees" value={attendees.toLocaleString('en-IN')} change="12.8%" foot="vs. last event" icon={Users} tone="mint"/><MetricCard label="Returning audience" value="68.4%" change="4.2%" foot="loyal listeners" icon={Heart} tone="coral"/><MetricCard label="Avg. watch time" value="48 min" change="11.2%" foot="per attendee" icon={AudioLines} tone="lilac"/><MetricCard label="Chat messages" value="3,842" change="22.1%" foot="this session" icon={MessageCircle} tone="yellow"/></section>
          <section className="audience-detail-grid"><div className="panel"><div className="panel-header"><div><div className="panel-overline">AUDIENCE MIX</div><h3>Where the crowd is from</h3></div></div>{[['Mumbai','28%'],['Delhi NCR','24%'],['Bengaluru','19%'],['Hyderabad','14%'],['Other cities','15%']].map(([name,pct],i)=><div className="city-row" key={name}><span>{name}</span><div className="city-track"><i style={{width:pct}}/></div><b>{pct}</b></div>)}</div><div className="panel"><div className="panel-overline">ENGAGEMENT</div><h3>What gets them going</h3><div className="engagement-stat"><span className="engagement-emoji">🔥</span><span><b>Fire reactions</b><small>Most used reaction</small></span><strong>8,420</strong></div><div className="engagement-stat"><span className="engagement-emoji">❤️</span><span><b>Heart reactions</b><small>Second most used</small></span><strong>6,180</strong></div><div className="engagement-stat"><span className="engagement-emoji">👏</span><span><b>Applause</b><small>Third most used</small></span><strong>4,920</strong></div></div></section>
        
    </>
  );
}
