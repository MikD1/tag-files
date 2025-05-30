import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {ReactiveFormsModule} from '@angular/forms';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {SearchService} from '../../services/search.service';
import {Router} from '@angular/router';

@Component({
  selector: 'app-search-bar',
  imports: [MatIconModule, MatButtonModule, ReactiveFormsModule],
  templateUrl: './search-bar.component.html',
  styleUrl: './search-bar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBarComponent {
  protected readonly searchService = inject(SearchService);
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

  protected clearSearch() {
    this.searchService.isImageSelected.update(() => false);
    this.searchService.isVideoSelected.update(() => false);
    this.searchService.searchQuery.setValue('');
    this.search();
  }
}
