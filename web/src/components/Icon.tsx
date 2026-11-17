import {
  ArrowLeft, ArrowUpDown, Bookmark, Check, ChevronLeft, ChevronRight, ChevronsUpDown, Clock,
  Ellipsis, Eye, Folder, Grid2X2, History, Image, LayoutGrid, List, Menu,
  Minus, Pencil, Play, Plus, RefreshCw, RotateCw, Search, Shuffle, SlidersHorizontal,
  Star, Tag, Upload, Video, X,
} from 'lucide-react';

const icons = {
  adjust: SlidersHorizontal,
  back: ArrowLeft,
  bookmark: Bookmark,
  check: Check,
  chevron: ChevronRight,
  'chevrons-up-down': ChevronsUpDown,
  clock: Clock,
  close: X,
  edit: Pencil,
  eye: Eye,
  folder: Folder,
  grid: Grid2X2,
  history: History,
  image: Image,
  library: LayoutGrid,
  list: List,
  menu: Menu,
  minus: Minus,
  more: Ellipsis,
  play: Play,
  plus: Plus,
  previous: ChevronLeft,
  retry: RefreshCw,
  rotate: RotateCw,
  search: Search,
  shuffle: Shuffle,
  sort: ArrowUpDown,
  star: Star,
  tag: Tag,
  upload: Upload,
  video: Video,
};

export type IconName = keyof typeof icons;

export function Icon({name, className}: {name: IconName; className?: string}) {
  const Glyph = icons[name];
  return <Glyph aria-hidden="true" className={className} strokeWidth={1.9}/>;
}
