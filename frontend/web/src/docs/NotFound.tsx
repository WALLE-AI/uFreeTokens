import React from 'react';
import { Link } from 'react-router';
import { useLocale, useT } from './i18n';

export const NotFound: React.FC = () => {
  const t = useT();
  const locale = useLocale();
  return (
    <div className="py-24 text-center">
      <p className="text-5xl font-extrabold text-gray-200">404</p>
      <h1 className="mt-4 text-xl font-bold text-gray-900">{t('notFound')}</h1>
      <p className="mt-2 text-sm text-gray-500">{t('notFoundBody')}</p>
      <Link to={`/docs/${locale}`} className="inline-block mt-6 text-sm font-semibold text-purple-700 hover:underline">
        {t('goHome')}
      </Link>
    </div>
  );
};
