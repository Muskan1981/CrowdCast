import React from 'react';
import { ArrowUpRight } from 'lucide-react';

export default function MetricCard({ label, value, change, icon: Icon, tone, foot }) {
  return (
    <div className="metric-card">
      <div className="metric-top">
        <span>{label}</span>
        <span className={`metric-icon ${tone}`}><Icon size={17} /></span>
      </div>
      <div className="metric-value">{value}</div>
      <div className="metric-foot">
        <span className="positive"><ArrowUpRight size={14} />{change}</span>
        <span>{foot}</span>
      </div>
    </div>
  );
}
