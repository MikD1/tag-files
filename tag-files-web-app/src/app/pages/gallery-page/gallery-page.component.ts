import { Component } from '@angular/core';
import { ImageGridComponent } from '../../components/image-grid/image-grid.component';

@Component({
  selector: 'app-gallery-page',
  imports: [ImageGridComponent],
  templateUrl: './gallery-page.component.html',
  styleUrl: './gallery-page.component.scss'
})
export class GalleryPageComponent {
}
