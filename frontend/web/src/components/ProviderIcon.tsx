import React, { useState } from 'react';
import { getProviderIconPath } from '../data/providerIcons';

interface ProviderIconProps {
  /** model.provider（或 author/org 前缀）——用来查真实厂商图标。 */
  provider: string;
  /** 外框尺寸/圆角，例如 "w-4 h-4 rounded"；需要包含尺寸类。 */
  className: string;
  /** 查不到真实图标时的占位样式（通常是 model.iconBg），和外框拼在一起。 */
  fallbackBg: string;
  /** 占位样式下显示的字符，默认三角形（历史上一直用这个占位）。 */
  fallbackGlyph?: string;
  /** 占位/真实图标的字号，配合 className 里的尺寸一起传，例如 "text-[9px]"。 */
  fallbackTextClassName?: string;
}

// ProviderIcon 统一了"模型库"里所有"厂商小图标"的渲染：查得到真实品牌图标
// （见 src/data/providerIcons.ts）就显示 SVG，查不到、或者图片加载失败，
// 回退成原来的纯色方块 + 三角形占位，行为对调用方完全透明——所有既有的
// model.iconBg 数据继续生效，不需要为每个模型手工补真实图标才能用。
export const ProviderIcon: React.FC<ProviderIconProps> = ({
  provider,
  className,
  fallbackBg,
  fallbackGlyph = '▲',
  fallbackTextClassName = '',
}) => {
  const iconPath = getProviderIconPath(provider);
  const [failed, setFailed] = useState(false);

  if (iconPath && !failed) {
    return (
      <span className={`${className} shrink-0 bg-white border border-gray-100 flex items-center justify-center p-[2px] overflow-hidden`}>
        <img
          src={iconPath}
          alt={provider}
          className="w-full h-full object-contain"
          loading="lazy"
          onError={() => setFailed(true)}
        />
      </span>
    );
  }

  return (
    <div className={`${className} ${fallbackTextClassName} shrink-0 flex items-center justify-center font-bold shadow-xs ${fallbackBg}`}>
      {fallbackGlyph}
    </div>
  );
};
