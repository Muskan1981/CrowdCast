export const trafficData = [
  { time: '18:00', requests: 320 }, { time: '18:05', requests: 440 },
  { time: '18:10', requests: 390 }, { time: '18:15', requests: 610 },
  { time: '18:20', requests: 540 }, { time: '18:25', requests: 780 },
  { time: '18:30', requests: 690 }, { time: '18:35', requests: 920 },
  { time: '18:40', requests: 860 }, { time: '18:45', requests: 1120 },
  { time: '18:50', requests: 1040 }, { time: '18:55', requests: 1280 },
];

export const serverRows = [
  { name: 'crowdcast-api-01', zone: 'Primary node', weight: 50, requests: '6,420', cpu: 42, memory: 58, status: 'Healthy', color: 'mint' },
  { name: 'crowdcast-api-02', zone: 'Secondary node', weight: 30, requests: '3,852', cpu: 28, memory: 46, status: 'Healthy', color: 'peach' },
  { name: 'crowdcast-api-03', zone: 'Burst node', weight: 20, requests: '2,568', cpu: 17, memory: 34, status: 'Healthy', color: 'lilac' },
];
