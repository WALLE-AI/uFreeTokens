import React from 'react';

const COLORS: Record<string, string> = {
  GET: 'text-emerald-700 bg-emerald-50 border-emerald-200',
  POST: 'text-blue-700 bg-blue-50 border-blue-200',
  PUT: 'text-amber-700 bg-amber-50 border-amber-200',
  PATCH: 'text-amber-700 bg-amber-50 border-amber-200',
  DELETE: 'text-rose-700 bg-rose-50 border-rose-200',
};

export const MethodBadge: React.FC<{ method: string; small?: boolean }> = ({ method, small }) => (
  <span
    className={`shrink-0 inline-flex justify-center font-mono font-bold border rounded ${
      small ? 'text-[9px] w-9 py-px' : 'text-[11px] px-1.5 py-0.5'
    } ${COLORS[method] ?? 'text-gray-700 bg-gray-50 border-gray-200'}`}
  >
    {method}
  </span>
);
