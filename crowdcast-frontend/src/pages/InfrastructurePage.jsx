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

const fmt = n => (n || 0).toLocaleString('en-IN');

export default function InfrastructurePage({ nodes, fleet, serverDown, toggleNode, pendingNode, showToast, refresh }) {
  const monitorStatus = name => fleet?.monitoring_services?.find(m => m.name === name)?.status || 'Unknown';
  return (
    <>

          <section className="page-heading"><div><div className="eyebrow"><span className="eyebrow-line"/> SYSTEM OBSERVABILITY</div><h1>Under the <em>hood.</em></h1><p className="subheading">A live look at the systems keeping your event running.</p></div><div className="heading-actions"><StatusPill good={!serverDown}>{fleet?.fleet_status === 'Operational' ? 'All systems healthy' : `Fleet ${fleet?.fleet_status || 'unknown'}`}</StatusPill><button className="button-secondary" onClick={()=>{refresh && refresh(); showToast('Metrics refreshed');}}><Activity size={15}/> Refresh metrics</button></div></section>
          <section className="metrics-grid infra-metrics"><MetricCard label="Total requests" value={fmt(fleet?.metrics?.total_requests)} change="live" foot="requests per second" icon={Activity} tone="mint"/><MetricCard label="Avg. latency" value={`${Math.round(fleet?.metrics?.avg_latency_ms || 0)} ms`} change="live" foot="app p95 window" icon={Gauge} tone="lilac"/><MetricCard label="Error rate" value={`${(fleet?.metrics?.error_rate_pct || 0).toFixed(2)}%`} change={serverDown ? 'Elevated' : 'live'} foot="5xx responses" icon={ShieldCheck} tone="coral"/><MetricCard label="Active connections" value={fmt(fleet?.metrics?.active_connections)} change="live" foot="across the fleet" icon={Wifi} tone="yellow"/></section>
          <section className="panel infra-panel"><div className="panel-header"><div><div className="panel-overline">COMPUTE FLEET</div><h3>Backend node management</h3></div><button className="button-secondary" disabled={!!pendingNode} onClick={()=>toggleNode()}><Zap size={15}/>{serverDown ? 'Restore backend 02' : 'Simulate node failure'}</button></div><div className="infra-table"><div className="infra-head"><span>NODE / INSTANCE</span><span>TRAFFIC WEIGHT</span><span>REQUESTS</span><span>CPU / MEMORY</span><span>HEALTH</span><span>ACTION</span></div>{nodes.map((s,i)=><div className="infra-row" key={s.name}><div className="node-name"><span className={`node-symbol ${s.color}`}><Server size={16}/></span><span><b>{s.name}</b><small>{s.zone}</small></span></div><div className="weight-cell"><b>{s.weight}%</b><div className="weight-track"><i style={{width:`${s.weight}%`}}/></div></div><b className="request-cell">{s.requests}</b><div className="resource-cell"><span>CPU {s.status !== 'Healthy' ? '—' : s.cpu+'%'}</span><span>RAM {s.status !== 'Healthy' ? '—' : s.memory+'%'}</span></div><StatusPill good={s.status === 'Healthy'}>{s.status === 'Healthy' ? 'Healthy' : 'Offline'}</StatusPill><button className="more-button" onClick={()=>showToast(`${s.name}: instance details`)}><MoreHorizontal size={18}/></button></div>)}</div><div className="infra-note"><ShieldCheck size={17}/><span>{serverDown ? 'Backend 02 is offline. In a real deployment, Nginx health checks should stop routing new requests to this instance.' : 'All instances are responding to health checks. Weighted round-robin routing is active.'}</span></div></section>
          <section className="infra-lower"><div className="panel"><div className="panel-header"><div><div className="panel-overline">RESOURCE UTILIZATION</div><h3>Node capacity</h3></div></div><div className="capacity-list">{nodes.map((s,i)=><div className="capacity-row" key={s.name}><div className="capacity-title"><b>{s.name}</b><span>{s.status !== 'Healthy' ? 'Offline' : `${s.cpu}% CPU`}</span></div><div className="capacity-track"><i style={{width:s.status !== 'Healthy' ? '0%' : `${s.cpu}%`}}/></div></div>)}</div></div><div className="panel stack-panel"><div className="panel-overline">OBSERVABILITY STACK</div><h3>Monitoring services</h3><div className="stack-service"><span className="stack-service-icon prometheus"><Activity size={17}/></span><span><b>Prometheus</b><small>Metrics collection · 5s scrape</small></span><StatusPill good={monitorStatus('Prometheus') === 'Up'}>{monitorStatus('Prometheus')}</StatusPill></div><div className="stack-service"><span className="stack-service-icon grafana"><BarChart3 size={17}/></span><span><b>Grafana</b><small>Dashboards · Port 3000</small></span><StatusPill good={monitorStatus('Grafana') === 'Up'}>{monitorStatus('Grafana')}</StatusPill></div></div></section>
        
    </>
  );
}
