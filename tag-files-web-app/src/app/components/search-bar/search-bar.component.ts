import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {ReactiveFormsModule} from '@angular/forms';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {MatMenuModule} from '@angular/material/menu';
import {SearchService} from '../../services/search.service';
import {Router} from '@angular/router';
import {SortType} from '../../services/api/sort-type';

@Component({
  selector: 'app-search-bar',
  imports: [MatIconModule, MatButtonModule, MatMenuModule, ReactiveFormsModule],
  templateUrl: './search-bar.component.html',
  styleUrl: './search-bar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBarComponent {
  protected readonly searchService = inject(SearchService);
  protected readonly SortType = SortType;
  private readonly router = inject(Router);

  protected search() {
    this.searchService.search();
    if (this.router.url !== '/library') {
      this.router.navigate(['/library']);
    }
  }

  protected toggleImage() {
    this.searchService.isImageSelected.update(prev => !prev);
    this.search();
  }

  protected toggleVideo() {
    this.searchService.isVideoSelected.update(prev => !prev);
    this.search();
  }

  protected selectSort(sort: SortType) {
    this.searchService.sortBy.update(_ => sort);
    this.search();
  }

  protected clearSearch() {
    this.searchService.isImageSelected.update(() => false);
    this.searchService.isVideoSelected.update(() => false);
    this.searchService.searchQuery.setValue('');
    this.search();
  }

  protected getSortIcon(): string {
    const sort = this.searchService.sortBy();
    switch (sort) {
      case SortType.UploadedAsc:
      case SortType.UploadedDesc:
        return 'history';
      case SortType.VideoDurationAsc:
      case SortType.VideoDurationDesc:
        return 'timelapse';
      case SortType.ViewCountAsc:
      case SortType.ViewCountDesc:
        return 'visibility';
      case SortType.Random:
        return 'shuffle';
      default:
        return 'sort';
    }
  }
}
