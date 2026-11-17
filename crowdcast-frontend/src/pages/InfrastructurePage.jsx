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

export default function InfrastructurePage({ serverDown, setServerDown, showToast }) {
  return (
    <>

          <section className="page-heading"><div><div className="eyebrow"><span className="eyebrow-line"/> SYSTEM OBSERVABILITY</div><h1>Under the <em>hood.</em></h1><p className="subheading">A live look at the systems keeping your event running.</p></div><div className="heading-actions"><StatusPill good={!serverDown}>{serverDown ? 'Degraded performance' : 'All systems healthy'}</StatusPill><button className="button-secondary" onClick={()=>showToast('Metrics refreshed')}><Activity size={15}/> Refresh metrics</button></div></section>
          <section className="metrics-grid infra-metrics"><MetricCard label="Total requests" value="12.8k" change="18.6%" foot="per minute" icon={Activity} tone="mint"/><MetricCard label="Avg. latency" value="42 ms" change="6.2%" foot="vs. previous hour" icon={Gauge} tone="lilac"/><MetricCard label="Error rate" value={serverDown ? '0.82%' : '0.04%'} change={serverDown ? 'Elevated' : '0.02%'} foot="5xx responses" icon={ShieldCheck} tone="coral"/><MetricCard label="Active connections" value="8,492" change="9.3%" foot="across 3 nodes" icon={Wifi} tone="yellow"/></section>
          <section className="panel infra-panel"><div className="panel-header"><div><div className="panel-overline">COMPUTE FLEET</div><h3>Backend node management</h3></div><button className="button-secondary" onClick={()=>{setServerDown(v=>!v);showToast(serverDown?'Backend 02 restored':'Backend 02 taken offline for failure test')}}><Zap size={15}/>{serverDown ? 'Restore backend 02' : 'Simulate node failure'}</button></div><div className="infra-table"><div className="infra-head"><span>NODE / INSTANCE</span><span>TRAFFIC WEIGHT</span><span>REQUESTS</span><span>CPU / MEMORY</span><span>HEALTH</span><span>ACTION</span></div>{serverRows.map((s,i)=><div className="infra-row" key={s.name}><div className="node-name"><span className={`node-symbol ${s.color}`}><Server size={16}/></span><span><b>{s.name}</b><small>{s.zone}</small></span></div><div className="weight-cell"><b>{s.weight}%</b><div className="weight-track"><i style={{width:`${s.weight}%`}}/></div></div><b className="request-cell">{serverDown&&i===1?'0':s.requests}</b><div className="resource-cell"><span>CPU {serverDown&&i===1?'—':s.cpu+'%'}</span><span>RAM {serverDown&&i===1?'—':s.memory+'%'}</span></div><StatusPill good={!(serverDown&&i===1)}>{serverDown&&i===1?'Offline':'Healthy'}</StatusPill><button className="more-button" onClick={()=>showToast(`${s.name}: instance details`)}><MoreHorizontal size={18}/></button></div>)}</div><div className="infra-note"><ShieldCheck size={17}/><span>{serverDown ? 'Backend 02 is offline. In a real deployment, Nginx health checks should stop routing new requests to this instance.' : 'All instances are responding to health checks. Weighted round-robin routing is active.'}</span></div></section>
          <section className="infra-lower"><div className="panel"><div className="panel-header"><div><div className="panel-overline">RESOURCE UTILIZATION</div><h3>Node capacity</h3></div></div><div className="capacity-list">{serverRows.map((s,i)=><div className="capacity-row" key={s.name}><div className="capacity-title"><b>{s.name}</b><span>{serverDown&&i===1?'Offline':`${s.cpu}% CPU`}</span></div><div className="capacity-track"><i style={{width:serverDown&&i===1?'0%':`${s.cpu}%`}}/></div></div>)}</div></div><div className="panel stack-panel"><div className="panel-overline">OBSERVABILITY STACK</div><h3>Monitoring services</h3><div className="stack-service"><span className="stack-service-icon prometheus"><Activity size={17}/></span><span><b>Prometheus</b><small>Metrics collection · 5s scrape</small></span><StatusPill>Running</StatusPill></div><div className="stack-service"><span className="stack-service-icon grafana"><BarChart3 size={17}/></span><span><b>Grafana</b><small>Dashboards · Port 3000</small></span><StatusPill>Running</StatusPill></div></div></section>
        
    </>
  );
}
