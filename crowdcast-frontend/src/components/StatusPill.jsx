import React from 'react';

export default function StatusPill({ children, good = true }) {
  return <span className={`status-pill ${good ? 'good' : 'bad'}`}><i />{children}</span>;
}
