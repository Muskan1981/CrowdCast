import React from 'react';
import { Check, X } from 'lucide-react';

export default function Toast({ message, onClose }) {
  if (!message) return null;
  return (
    <div className="toast">
      <span><Check size={15} /></span>{message}
      <button onClick={onClose}><X size={14} /></button>
    </div>
  );
}
