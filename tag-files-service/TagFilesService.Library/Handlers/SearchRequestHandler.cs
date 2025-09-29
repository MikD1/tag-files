using System.Linq.Dynamic.Core;
using MediatR;
using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Library.Contracts;
using TagFilesService.Model;

namespace TagFilesService.Library.Handlers;

public class SearchRequestHandler(AppDbContext dbContext)
    : IRequestHandler<SearchRequest, PaginatedList<LibraryItemDto>>
{
    public async Task<PaginatedList<LibraryItemDto>> Handle(SearchRequest request, CancellationToken cancellationToken)
    {
        PaginatedList<LibraryItem> searchResults = await Search(request);
        List<LibraryItemDto> itemsDto = searchResults.Items
            .Select(LibraryItemDto.FromModel)
            .ToList();
        return new(itemsDto, searchResults.TotalItems, searchResults.PageIndex, searchResults.TotalPages);
    }

    private async Task<PaginatedList<LibraryItem>> Search(SearchRequest request)
    {
        IQueryable<LibraryItem> query = dbContext.LibraryItems
            .Include(x => x.Tags);

        if (!string.IsNullOrWhiteSpace(request.TagQuery))
        {
            string dynamicQuery = TagQueryConverter.ConvertToDynamicQuery(request.TagQuery);
            query = query
                .Where(dynamicQuery);
        }

        if (request.ItemType is not null)
        {
            query = query.Where(x => x.FileType == request.ItemType);
        }

        query = request.SortBy switch
        {
            SortType.UploadedAsc => query.OrderBy(x => x.UploadedOn),
            SortType.VideoDurationDesc => query.OrderByDescending(x => x.VideoDuration),
            SortType.VideoDurationAsc => query.OrderBy(x => x.VideoDuration),
            SortType.ViewCountDesc => query.OrderByDescending(x => x.ViewCount),
            SortType.ViewCountAsc => query.OrderBy(x => x.ViewCount),
            SortType.Random => query.OrderBy(x => EF.Functions.Random()),
            _ => query.OrderByDescending(x => x.UploadedOn)
        };
        return await QueryablePaginatedList<LibraryItem>.CreateAsync(query, request.PageIndex, request.PageSize);
    }
}