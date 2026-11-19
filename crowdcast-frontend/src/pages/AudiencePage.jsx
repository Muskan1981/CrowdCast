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

export default function AudiencePage({ attendees, reactions, messages, showToast, audience }) {
  return (
    <>

          <section className="page-heading"><div><div className="eyebrow"><span className="eyebrow-line"/> YOUR COMMUNITY</div><h1>People make the <em>moment.</em></h1><p className="subheading">A snapshot of the crowd showing up for After Hours.</p></div><div className="heading-actions"><button className="button-secondary" onClick={()=>showToast('Audience report exported (demo)')}><BarChart3 size={15}/> Export report</button></div></section>
          <section className="metrics-grid"><MetricCard label="Total attendees" value={attendees.toLocaleString('en-IN')} change={audience?.metrics?.total_attendees?.change || '—'} foot={audience?.metrics?.total_attendees?.foot || 'all time'} icon={Users} tone="mint"/><MetricCard label="Returning audience" value={audience?.metrics?.returning_audience?.value || '—'} change={audience?.metrics?.returning_audience?.change || '—'} foot={audience?.metrics?.returning_audience?.foot || ''} icon={Heart} tone="coral"/><MetricCard label="Avg. watch time" value={audience?.metrics?.avg_watch_time?.value || '—'} change={audience?.metrics?.avg_watch_time?.change || '—'} foot={audience?.metrics?.avg_watch_time?.foot || ''} icon={AudioLines} tone="lilac"/><MetricCard label="Chat messages" value={audience?.metrics?.chat_messages?.value || '—'} change={audience?.metrics?.chat_messages?.change || '—'} foot={audience?.metrics?.chat_messages?.foot || ''} icon={MessageCircle} tone="yellow"/></section>
          <section className="audience-detail-grid"><div className="panel"><div className="panel-header"><div><div className="panel-overline">AUDIENCE MIX</div><h3>Where the crowd is from</h3></div></div>{(audience?.city_mix || []).map(({name,percentage}) => <div className="city-row" key={name}><span>{name === 'Other' ? 'Other cities' : name}</span><div className="city-track"><i style={{width:percentage}}/></div><b>{percentage}</b></div>)}</div><div className="panel"><div className="panel-overline">ENGAGEMENT</div><h3>What gets them going</h3>{(audience?.top_reactions || []).map(r => <div className="engagement-stat" key={r.emoji}><span className="engagement-emoji">{r.emoji}</span><span><b>{r.name} reactions</b><small>{r.subtitle}</small></span><strong>{r.count}</strong></div>)}</div></section>
        
    </>
  );
}
