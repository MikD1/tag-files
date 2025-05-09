import {ChangeDetectionStrategy, Component, inject, signal} from '@angular/core';
import {FormControl, ReactiveFormsModule} from '@angular/forms';
import {MatButtonModule} from '@angular/material/button';
import {MatIconModule} from '@angular/material/icon';
import {SearchService} from '../../services/search.service';
import {FileType} from '../../services/api/library-api.service';

@Component({
  selector: 'app-search-bar',
  imports: [MatIconModule, MatButtonModule, ReactiveFormsModule],
  templateUrl: './search-bar.component.html',
  styleUrl: './search-bar.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SearchBarComponent {
  protected readonly searchQuery = new FormControl<string>('');
  protected readonly isImageSelected = signal<boolean>(false);
  protected readonly isVideoSelected = signal<boolean>(false);
  private readonly searchService = inject(SearchService);

  protected search() {
    const searchQuery = this.searchQuery.value ? this.searchQuery.value : undefined;

    let fileType = undefined
    if (this.isImageSelected() && !this.isVideoSelected()) {
      fileType = FileType.Image;
    }
    if (!this.isImageSelected() && this.isVideoSelected()) {
      fileType = FileType.Video;
    }

    this.searchService.search(searchQuery, fileType);
  }

  protected toggleImage() {
    this.isImageSelected.update(prev => !prev);
    this.search();
  }

  protected toggleVideo() {
    this.isVideoSelected.update(prev => !prev);
    this.search();
  }
}
