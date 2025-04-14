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
        PaginatedList<FileMetadata> searchResults = await Search(request.TagQuery, request.PageIndex, request.PageSize);
        List<LibraryItemDto> itemsDto = searchResults.Items
            .Select(LibraryItemDto.FromMetadata)
            .ToList();
        return new(itemsDto, searchResults.TotalItems, searchResults.PageIndex, searchResults.TotalPages);
    }

    private async Task<PaginatedList<FileMetadata>> Search(string tagQuery, int pageIndex, int pageSize)
    {
        IQueryable<FileMetadata> queryable = dbContext.FilesMetadata
            .Include(x => x.Tags);

        if (!string.IsNullOrWhiteSpace(tagQuery))
        {
            string dynamicQuery = TagQueryConverter.ConvertToDynamicQuery(tagQuery);
            queryable = queryable
                .Where(dynamicQuery);
        }

        queryable = queryable
            .OrderByDescending(x => x.UploadedOn);
        return await QueryablePaginatedList<FileMetadata>.CreateAsync(queryable, pageIndex, pageSize);
    }
}