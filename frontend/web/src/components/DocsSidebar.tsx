import React, { useState } from 'react';
import {
  Search,
  ChevronRight,
  ChevronDown,
  X
} from 'lucide-react';
import { DOCS_SECTIONS, DocItem, DocSection } from '../data/docsNavigationData';

interface DocsSidebarProps {
  activeDocId: string;
  onSelectDoc: (id: string, endpointId?: string) => void;
  className?: string;
  onCloseMobile?: () => void;
}

export const DocsSidebar: React.FC<DocsSidebarProps> = ({
  activeDocId,
  onSelectDoc,
  className = '',
  onCloseMobile
}) => {
  const [searchQuery, setSearchQuery] = useState('');
  const [expandedItems, setExpandedItems] = useState<Record<string, boolean>>({
    multimodal: false,
    'model-variants': false,
    routers: false,
    'server-tools': false,
    plugins: false,
    workspaces: false,
    guardrails: false,
    broadcast: false
  });

  const toggleExpand = (id: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setExpandedItems((prev) => ({
      ...prev,
      [id]: !prev[id]
    }));
  };

  const handleItemClick = (item: DocItem) => {
    onSelectDoc(item.id, item.endpointId);
    if (item.hasChevron && !expandedItems[item.id]) {
      setExpandedItems((prev) => ({ ...prev, [item.id]: true }));
    }
    if (onCloseMobile) {
      onCloseMobile();
    }
  };

  const handleSubItemClick = (parentId: string, subId: string, endpointId?: string, e?: React.MouseEvent) => {
    if (e) e.stopPropagation();
    onSelectDoc(`${parentId}:${subId}`, endpointId);
    if (onCloseMobile) {
      onCloseMobile();
    }
  };

  // Filter sections based on search query
  const filteredSections = DOCS_SECTIONS.map((sec) => {
    if (!searchQuery.trim()) return sec;
    const q = searchQuery.toLowerCase();
    const matchingItems = sec.items.filter((item) => {
      const matchTitle = item.title.toLowerCase().includes(q);
      const matchDesc = item.description?.toLowerCase().includes(q);
      const matchSubs = item.subItems?.some((sub) => sub.title.toLowerCase().includes(q));
      return matchTitle || matchDesc || matchSubs;
    });
    return {
      ...sec,
      items: matchingItems
    };
  }).filter((sec) => sec.items.length > 0);

  return (
    <aside
      className={`bg-[#090a0f] text-[#9ca3af] border-r border-[#181a24] flex flex-col select-none ${className}`}
    >
      {/* Search Input Box */}
      <div className="p-3 border-b border-[#181a24] sticky top-0 bg-[#090a0f] z-10">
        <div className="relative flex items-center">
          <Search className="w-3.5 h-3.5 absolute left-2.5 text-[#6b7280] pointer-events-none" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="搜索文档与规范..."
            className="w-full bg-[#11131a] border border-[#202330] rounded-md pl-8 pr-7 py-1.5 text-xs text-gray-200 placeholder-[#525866] focus:outline-none focus:border-purple-500/70 focus:bg-[#151722] transition-colors"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute right-2 text-gray-500 hover:text-gray-300 p-0.5 cursor-pointer"
            >
              <X className="w-3 h-3" />
            </button>
          )}
        </div>
      </div>

      {/* Navigation Tree */}
      <div className="flex-1 overflow-y-auto px-2.5 py-3 space-y-4 text-[13px] custom-scrollbar">
        {filteredSections.map((section) => (
          <div key={section.id} className="space-y-0.5">
            {/* Section Header */}
            {section.title && (
              <div className="flex items-center gap-2 text-white font-semibold px-2 pt-3 pb-1 text-[13px] tracking-tight">
                {section.icon && (
                  <section.icon className="w-4 h-4 text-gray-300 shrink-0" />
                )}
                {section.isOriHeader && (
                  <span className="font-mono text-purple-400 text-xs font-bold">&gt;_</span>
                )}
                <span>{section.title}</span>
              </div>
            )}

            {/* Section Items */}
            <div className="space-y-0.5">
              {section.items.map((item) => {
                const isSelected =
                  activeDocId === item.id || activeDocId.startsWith(`${item.id}:`);
                const isQuick = item.isQuickstart;
                const isExpanded = expandedItems[item.id];
                const Icon = item.icon;

                return (
                  <div key={item.id} className="space-y-0.5">
                    <button
                      type="button"
                      onClick={() => handleItemClick(item)}
                      className={`w-full group text-left px-2.5 py-1.5 rounded-lg flex items-center gap-2.5 transition-colors cursor-pointer ${
                        isQuick && isSelected
                          ? 'bg-[#1c260d] text-[#bbf438] font-medium border border-[#2e4014]'
                          : isQuick
                          ? 'bg-[#161e0b] text-[#a3e635] hover:bg-[#1c260d] font-medium'
                          : isSelected
                          ? 'bg-[#181a24] text-white font-medium border border-[#262835]'
                          : 'text-[#9ca3af] hover:text-white hover:bg-[#12141c]'
                      }`}
                    >
                      <Icon
                        className={`w-4 h-4 shrink-0 ${
                          isQuick
                            ? 'text-[#a3e635]'
                            : isSelected
                            ? 'text-white'
                            : 'text-[#717684] group-hover:text-gray-200'
                        }`}
                      />
                      <span className="truncate flex-1">{item.title}</span>

                      {/* Beta badge */}
                      {item.badge && (
                        <span className="bg-[#1d2028] text-[#8e95a5] border border-[#2b2e3a] text-[10px] font-mono px-1.5 py-0.2 rounded shrink-0 mr-0.5">
                          {item.badge}
                        </span>
                      )}

                      {/* Expandable Chevron */}
                      {item.hasChevron && (
                        <span
                          onClick={(e) => toggleExpand(item.id, e)}
                          className="p-0.5 hover:text-white text-[#5b6170] transition-transform shrink-0"
                          title="展开子项"
                        >
                          {isExpanded ? (
                            <ChevronDown className="w-3.5 h-3.5" />
                          ) : (
                            <ChevronRight className="w-3.5 h-3.5" />
                          )}
                        </span>
                      )}
                    </button>

                    {/* Sub-items accordion */}
                    {item.hasChevron && isExpanded && item.subItems && (
                      <div className="pl-6 pr-1 py-0.5 space-y-0.5 border-l border-[#202330] ml-4 my-0.5">
                        {item.subItems.map((sub) => {
                          const isSubSelected = activeDocId === `${item.id}:${sub.id}`;
                          return (
                            <button
                              key={sub.id}
                              type="button"
                              onClick={(e) => handleSubItemClick(item.id, sub.id, sub.endpointId, e)}
                              className={`w-full text-left px-2 py-1 rounded text-xs transition-colors flex items-center justify-between cursor-pointer ${
                                isSubSelected
                                  ? 'bg-[#1d202c] text-purple-300 font-medium'
                                  : 'text-[#858b99] hover:text-gray-200 hover:bg-[#141620]'
                              }`}
                            >
                              <span className="truncate">{sub.title}</span>
                              {sub.badge && (
                                <span className="text-[9px] bg-purple-950 text-purple-300 px-1 rounded">
                                  {sub.badge}
                                </span>
                              )}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </aside>
  );
};
